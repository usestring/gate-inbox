package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

// JEV copy acts when the operator presses its key. It reads the latest
// assistant message of the session the key was pressed on, picks out the
// text an operator might lift from it -- fenced blocks, code spans and
// links -- and asks JEV which of them the agent left for the operator to use
// themselves: a command to run in their own terminal, a link to open, a value
// to paste into a form. The first pick goes on the clipboard, and pressing
// the key again on the same turn walks through the rest in the order the
// agent wrote them, so one message's steps come off in sequence.
//
// Nothing is read or sent until the key is pressed, and nothing is written
// to the clipboard except on that key.

const jevCopySetting = "experimental_jev_copy"

const (
	jevCopyThreshold    = 0.80
	jevCopyCandidates   = 8
	jevCopyCandidateMax = 1000
	jevCopyMinChars     = 4
	jevCopyMessageChars = 1500
	jevCopyTimeout      = 6 * time.Second
)

// jevCopyQuestion is asked once per candidate, with the candidate quoted.
const jevCopyQuestion = "Does the latest assistant message ask the user to copy this exact text and use it themselves: " +
	"run it in their own terminal, open it in a browser, or paste it into a form, chat or another app? " +
	"Answer no for text that only explains or names something, and for commands the agent runs itself. Text: "

var (
	copyFencePattern             = regexp.MustCompile("(?s)```[^\\n`]*\\n(.*?)```")
	copySpanPattern              = regexp.MustCompile("`([^`\\n]+)`")
	copyURLPattern               = regexp.MustCompile(`https?://[^\s<>\[\]"'` + "`" + `]+`)
	copyProseContinuationPattern = regexp.MustCompile(`^[ \t]+\p{L}`)
	copyCandidatePattern         = regexp.MustCompile(copyFencePattern.String() + "|" + copySpanPattern.String() + "|(" + copyURLPattern.String() + ")")
)

// jevCopyState is what the copy key remembers between presses: the picks JEV
// made from one turn and which of them goes on the clipboard next. turn names
// the turn they came from, so a press on another row or a later turn reads
// afresh while a press on the same turn costs no call.
type jevCopyState struct {
	generation uint64
	busy       bool
	copying    bool
	turn       string
	from       string
	picks      []string
	next       int
}

type jevCopyResultMsg struct {
	generation uint64
	turn       string
	from       string
	texts      []string
	// note says why there is nothing to copy when texts is empty.
	note string
}

type jevCopiedMsg struct {
	generation uint64
	from       string
	index      int
	total      int
	chars      int
	err        error
}

func storedJevCopy(st *store.Store) bool { return storedExperiment(st, jevCopySetting) }

// copyCandidates is the text in message an operator might lift, in the
// order it appears: fenced blocks, code spans and links outside them.
// Text too short to be worth a key, or too long to send, is left out
// rather than cut, since a truncated command is worse than none.
func copyCandidates(message string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(text string) {
		text = strings.TrimSpace(text)
		n := utf8.RuneCountInString(text)
		if n < jevCopyMinChars || n > jevCopyCandidateMax || seen[text] || len(out) >= jevCopyCandidates {
			return
		}
		seen[text] = true
		out = append(out, text)
	}
	for _, match := range copyCandidatePattern.FindAllStringSubmatchIndex(message, -1) {
		switch {
		case match[2] >= 0:
			add(message[match[2]:match[3]])
		case match[4] >= 0:
			add(message[match[4]:match[5]])
		case match[6] >= 0:
			start, end := match[6], match[7]
			link := message[start:end]
			if (start == 0 || message[start-1] != '<' && message[start-1] != '(') &&
				copyProseContinuationPattern.MatchString(message[end:]) {
				link = strings.TrimRight(link, ".,;:!?")
			}
			depth := 0
			for i, char := range link {
				if char == '(' {
					depth++
				} else if char == ')' {
					if depth == 0 {
						link = link[:i]
						break
					}
					depth--
				}
			}
			add(link)
		}
	}
	return out
}

// latestAssistantText is the closing assistant message of a turn, or empty
// when the transcript ends on anything else.
func latestAssistantText(messages []search.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		switch messages[i].Role {
		case "assistant":
			return messages[i].Text
		case "user":
			return ""
		}
	}
	return ""
}

// askJevCopy asks one yes-no question per candidate in one call and returns
// the candidates JEV is confident the operator will copy.
func askJevCopy(ctx context.Context, key, message string, candidates []string) ([]string, bool) {
	questions := make(map[string]string, len(candidates))
	for i, candidate := range candidates {
		questions[fmt.Sprintf("c%d", i+1)] = jevCopyQuestion + candidate
	}
	answers, ok := askJevNoul(ctx, jevHTTPClient, jevEndpoint, key,
		map[string]any{"message": boundedText(message, jevCopyMessageChars)}, questions)
	if !ok {
		return nil, false
	}
	var picked []string
	for i, candidate := range candidates {
		if answers[fmt.Sprintf("c%d", i+1)] >= jevCopyThreshold {
			picked = append(picked, candidate)
		}
	}
	return picked, true
}

func jevCopyIdentity(sess store.Session) string {
	return jevFinishIdentity(sess) + "\x00" + sess.AgentSessionID
}

// copySelectedWithJev runs the copy key on the list's selected row.
func (m *Model) copySelectedWithJev() (tea.Model, tea.Cmd) {
	sess, ok := m.selected()
	if !ok || sess.Archived {
		return m, nil
	}
	return m.copyWithJev(sess)
}

// copyWithJev puts the next pick from sess's latest turn on the clipboard,
// first reading that turn and asking JEV, off the event loop, when the picks
// on hand came from another one.
func (m *Model) copyWithJev(sess store.Session) (tea.Model, tea.Cmd) {
	if !m.jevCopyOn || m.jevCopy.busy || m.jevCopy.copying {
		return m, nil
	}
	turn := jevCopyIdentity(sess)
	if turn == m.jevCopy.turn && len(m.jevCopy.picks) > 0 {
		return m, m.copyNextPick()
	}
	key, _ := m.jevKey()
	if key == "" {
		m.errBar.text = "JEV copy needs a TypeSafe key (Settings → JEV, or TYPESAFE_API_KEY)"
		return m, nil
	}
	if m.conversation == nil {
		return m, nil
	}
	m.jevCopy.busy = true
	locator := search.NewLocator(m.conversation.locator.ClaudeHome, m.conversation.locator.CodexRoot)
	tool := historyToolFormats(m.cfg)[sess.Tool]
	database := opencodeDBPath()
	from := m.displayName(sess)
	generation := m.jevCopy.generation
	m.reportDone("JEV is reading " + from + "'s last message")
	return m, func() tea.Msg {
		msg := jevCopyResultMsg{generation: generation, turn: turn, from: from}
		target, found := locator.Target(sess.ID, tool, sess.Cwd, sess.AgentSessionID)
		if !found {
			msg.note = "no conversation found for " + from
			return msg
		}
		messages, err := search.ReadMessages(target, database)
		if err != nil {
			msg.note = "could not read " + from + ": " + err.Error()
			return msg
		}
		text := latestAssistantText(messages)
		candidates := copyCandidates(text)
		if len(candidates) == 0 {
			msg.note = "nothing to copy in " + from + "'s last message"
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), jevCopyTimeout)
		defer cancel()
		texts, ok := askJevCopy(ctx, key, text, candidates)
		switch {
		case !ok:
			msg.note = "JEV did not answer; nothing copied"
		case len(texts) == 0:
			msg.note = "JEV found nothing in " + from + "'s last message left for you to copy"
		}
		msg.texts = texts
		return msg
	}
}

func (m *Model) applyJevCopy(msg jevCopyResultMsg) tea.Cmd {
	m.jevCopy.busy = false
	if !m.jevCopyOn || msg.generation != m.jevCopy.generation {
		return nil
	}
	if len(msg.texts) == 0 {
		m.errBar.text = msg.note
		return nil
	}
	logging.Info("jev copy", "from", msg.from, "picks", len(msg.texts))
	m.jevCopy.turn, m.jevCopy.from, m.jevCopy.picks, m.jevCopy.next = msg.turn, msg.from, msg.texts, 0
	return m.copyNextPick()
}

// copyNextPick writes the next pick to the clipboard, wrapping to the first
// after the last. One write at a time, so a slow clipboard cannot land an
// older pick over a newer one.
func (m *Model) copyNextPick() tea.Cmd {
	m.jevCopy.copying = true
	index := m.jevCopy.next
	text := m.jevCopy.picks[index]
	m.jevCopy.next = (index + 1) % len(m.jevCopy.picks)
	msg := jevCopiedMsg{generation: m.jevCopy.generation, from: m.jevCopy.from, index: index, total: len(m.jevCopy.picks)}
	return func() tea.Msg {
		if err := writeClipboard(text); err != nil {
			msg.err = err
			return msg
		}
		msg.chars = utf8.RuneCountInString(text)
		return msg
	}
}

func (m *Model) applyJevCopied(msg jevCopiedMsg) {
	m.jevCopy.copying = false
	if !m.jevCopyOn || msg.generation != m.jevCopy.generation {
		return
	}
	if msg.err != nil {
		// The same pick is still wanted: the next press tries it again.
		m.jevCopy.next = msg.index
		m.errBar.text = "could not copy: " + msg.err.Error()
		return
	}
	text := fmt.Sprintf("copied %d chars from %s", msg.chars, msg.from)
	if msg.total > 1 {
		text += fmt.Sprintf(" (%d of %d)", msg.index+1, msg.total)
		if key := m.copyKeyName(); key != "" {
			text += " · " + key + " copies the next"
		}
	}
	m.reportDone(text)
}

// copyKeyName is the copy key on the screen the operator is on.
func (m *Model) copyKeyName() string {
	screen := keymap.ContextList
	if m.mode == modeFocus {
		screen = keymap.ContextFocus
	}
	return m.fullCap(screen, keymap.CopyJev)
}
