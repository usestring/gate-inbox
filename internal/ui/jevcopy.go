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
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The JEV copy queue reads a session's latest assistant message each time
// its row turns waiting or finished, picks out the text an operator might
// lift from it -- fenced blocks, code spans and links -- and asks JEV which
// of them the operator will need on the clipboard: a command to run
// themselves, a link to open, a value to paste into a form. Those land on a
// queue the copy key drains oldest first, so the steps of one message come
// off in the order the agent wrote them.
//
// Nothing is written to the clipboard until the operator asks: a board that
// overwrote it on every turn end would take away whatever they had copied
// themselves.

const jevCopyQueueSetting = "experimental_jev_copy_queue"

const (
	jevCopyThreshold    = 0.80
	jevCopyCandidates   = 8
	jevCopyCandidateMax = 1000
	jevCopyMinChars     = 4
	jevCopyMessageChars = 1500
	jevCopyQueueCap     = 20
	jevCopyTimeout      = 6 * time.Second
)

// jevCopyQuestion is asked once per candidate, with the candidate quoted.
const jevCopyQuestion = "Does the latest assistant message ask the user to copy this exact text and use it themselves: " +
	"run it in their own terminal, open it in a browser, or paste it into a form, chat or another app? " +
	"Answer no for text that only explains or names something, and for commands the agent runs itself. Text: "

var (
	copyFencePattern     = regexp.MustCompile("(?s)```[^\\n`]*\\n(.*?)```")
	copySpanPattern      = regexp.MustCompile("`([^`\\n]+)`")
	copyURLPattern       = regexp.MustCompile(`https?://[^\s<>\[\]"'` + "`" + `]+`)
	copyCandidatePattern = regexp.MustCompile(copyFencePattern.String() + "|" + copySpanPattern.String() + "|(" + copyURLPattern.String() + ")")
)

// copyItem is one queued piece of text and the session it came from.
type copyItem struct {
	from string
	text string
}

// jevCopyState is what the queue remembers between passes. checked holds
// the turn end each row was last read at, so one turn costs one call;
// seeded marks the first pass, which records every row's current turn
// without asking, so turning the feature on does not reread the whole board.
type jevCopyState struct {
	busy    bool
	seeded  bool
	checked map[string]string
	queue   []copyItem
}

type jevCopyResultMsg struct {
	from  string
	texts []string
}

type jevCopiedMsg struct {
	item  copyItem
	chars int
	err   error
}

func storedJevCopyQueue(st *store.Store) bool { return storedExperiment(st, jevCopyQueueSetting) }

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
	for _, match := range copyCandidatePattern.FindAllStringSubmatch(message, -1) {
		switch {
		case match[1] != "":
			add(match[1])
		case match[2] != "":
			add(match[2])
		case match[3] != "":
			link := match[3]
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
			add(strings.TrimRight(link, ".,;:!?"))
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

// jevCopyCandidate picks the next row whose turn ended since it was last
// read.
func (m *Model) jevCopyCandidate() (store.Session, string, bool) {
	for _, sess := range m.sessions {
		if sess.Archived || sess.Status != status.Waiting && sess.Status != status.Finished {
			continue
		}
		identity := jevFinishIdentity(sess)
		if m.jevCopy.checked[sess.ID] == identity {
			continue
		}
		return sess, identity, true
	}
	return store.Session{}, "", false
}

// checkCopyWithJev reads one turn end per poll pass, off the event loop.
func (m *Model) checkCopyWithJev() tea.Cmd {
	key, _ := m.jevKey()
	if !m.jevCopyQueue || key == "" || m.jevCopy.busy || m.conversation == nil {
		return nil
	}
	if m.jevCopy.checked == nil {
		m.jevCopy.checked = map[string]string{}
	}
	if !m.jevCopy.seeded {
		m.jevCopy.seeded = true
		for _, sess := range m.sessions {
			m.jevCopy.checked[sess.ID] = jevFinishIdentity(sess)
		}
		return nil
	}
	sess, identity, ok := m.jevCopyCandidate()
	if !ok {
		return nil
	}
	m.jevCopy.checked[sess.ID] = identity
	m.jevCopy.busy = true
	locator := search.NewLocator(m.conversation.locator.ClaudeHome, m.conversation.locator.CodexRoot)
	tool := historyToolFormats(m.cfg)[sess.Tool]
	database := opencodeDBPath()
	from := m.displayName(sess)
	return func() tea.Msg {
		msg := jevCopyResultMsg{from: from}
		target, found := locator.Target(sess.ID, tool, sess.Cwd, sess.AgentSessionID)
		if !found {
			return msg
		}
		messages, err := search.ReadMessages(target, database)
		if err != nil {
			return msg
		}
		text := latestAssistantText(messages)
		candidates := copyCandidates(text)
		if len(candidates) == 0 {
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), jevCopyTimeout)
		defer cancel()
		msg.texts, _ = askJevCopy(ctx, key, text, candidates)
		return msg
	}
}

func (m *Model) applyJevCopy(msg jevCopyResultMsg) {
	m.jevCopy.busy = false
	if !m.jevCopyQueue || len(msg.texts) == 0 {
		return
	}
	added := 0
	for _, text := range msg.texts {
		if m.queuedForCopy(text) {
			continue
		}
		m.jevCopy.queue = append(m.jevCopy.queue, copyItem{from: msg.from, text: text})
		added++
	}
	if over := len(m.jevCopy.queue) - jevCopyQueueCap; over > 0 {
		m.jevCopy.queue = m.jevCopy.queue[over:]
	}
	if added == 0 {
		return
	}
	logging.Info("jev copy queue", "from", msg.from, "added", added, "queued", len(m.jevCopy.queue))
	m.reportDone(fmt.Sprintf("JEV queued %d from %s to copy · %s", added, msg.from, m.copyQueueHint()))
}

func (m *Model) queuedForCopy(text string) bool {
	for _, item := range m.jevCopy.queue {
		if item.text == text {
			return true
		}
	}
	return false
}

// copyQueueHint names the queue's length and the key that drains it.
func (m *Model) copyQueueHint() string {
	hint := fmt.Sprintf("%d in the copy queue", len(m.jevCopy.queue))
	screen := keymap.ContextList
	if m.mode == modeFocus {
		screen = keymap.ContextFocus
	}
	if key := m.fullCap(screen, keymap.CopyQueued); key != "" {
		hint += ", " + key + " copies the next"
	}
	return hint
}

// copyNextQueued takes the oldest queued text off the queue and puts it on
// the clipboard.
func (m *Model) copyNextQueued() (tea.Model, tea.Cmd) {
	if len(m.jevCopy.queue) == 0 {
		m.errBar.text = "the copy queue is empty"
		return m, nil
	}
	item := m.jevCopy.queue[0]
	m.jevCopy.queue = m.jevCopy.queue[1:]
	return m, func() tea.Msg {
		if err := writeClipboard(item.text); err != nil {
			return jevCopiedMsg{item: item, err: err}
		}
		return jevCopiedMsg{item: item, chars: utf8.RuneCountInString(item.text)}
	}
}

func (m *Model) applyJevCopied(msg jevCopiedMsg) {
	if msg.err != nil {
		// The text stays wanted: put it back at the front for another try.
		m.jevCopy.queue = append([]copyItem{msg.item}, m.jevCopy.queue...)
		m.errBar.text = "could not copy: " + msg.err.Error()
		return
	}
	m.reportDone(fmt.Sprintf("copied %d chars from %s · %d left in the copy queue", msg.chars, msg.item.from, len(m.jevCopy.queue)))
}
