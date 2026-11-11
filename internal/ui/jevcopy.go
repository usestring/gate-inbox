package ui

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/autocopy"
	"github.com/usestring/gate-inbox/internal/classifier"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

// Smart copy acts when the operator presses its key. It reads the latest
// assistant message of the session the key was pressed on, picks out the
// text an operator might lift from it -- drafts, fenced blocks, code spans and
// links -- and asks the classifier which of them the agent left for the operator to use
// themselves: a command to run in their own terminal, a link to open, a value
// to paste into a form. The first pick goes on the clipboard, and pressing
// the key again on the same turn walks through the rest in the order the
// agent wrote them, so one message's steps come off in sequence.
//
// Which text is offered and how the classifier's answers become picks belongs to the
// autocopy package, which carries the evals; this file only asks the classifier and
// drives the clipboard.
//
// Nothing is read or sent until the key is pressed, and nothing is written
// to the clipboard except on that key.

const jevCopySetting = "experimental_jev_copy"

const jevCopyTimeout = 6 * time.Second

// jevCopyState is what the copy key remembers between presses: the picks the classifier
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
	note       string
	next       int
}

type jevCopyResultMsg struct {
	generation uint64
	turn       string
	from       string
	texts      []string
	ok         bool
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

// askJevCopy asks the classifier every question plan carries in one call and returns its
// picks, with adjacent draft paragraphs kept together so the clipboard holds
// the reply rather than its pieces. ok is false when the classifier did not answer them all.
func askJevCopy(ctx context.Context, provider classifier.Provider, key string, plan *autocopy.Plan) ([]string, bool) {
	input := plan.Input()
	answers, ok := askJevNoul(ctx, provider, jevHTTPClient, provider.Endpoint(), key,
		map[string]any{"message": input.Message}, input.Questions)
	if !ok {
		return nil, false
	}
	picks, err := plan.Select(answers)
	return picks, err == nil
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
// first reading that turn and asking the classifier, off the event loop, when the picks
// on hand came from another one.
func (m *Model) copyWithJev(sess store.Session) (tea.Model, tea.Cmd) {
	if !m.jevCopyOn || m.jevCopy.busy || m.jevCopy.copying {
		return m, nil
	}
	turn := jevCopyIdentity(sess)
	if turn == m.jevCopy.turn {
		if len(m.jevCopy.picks) == 0 {
			m.errBar.text = m.jevCopy.note
			return m, nil
		}
		return m, m.copyNextPick()
	}
	key := m.jevAPIKey()
	provider := m.classifierProvider()
	if key == "" {
		m.errBar.text = "Copy needs a configured provider (Settings → Classifier)"
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
	m.reportDone(provider.Name() + " is reading " + from + "'s last message")
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
		plan := autocopy.Prepare(text)
		if len(plan.Input().Questions) == 0 {
			msg.ok = true
			msg.note = "nothing to copy in " + from + "'s last message"
			return msg
		}
		ctx, cancel := context.WithTimeout(context.Background(), jevCopyTimeout)
		defer cancel()
		texts, ok := askJevCopy(ctx, provider, key, plan)
		switch {
		case !ok:
			msg.note = provider.Name() + " did not answer; nothing copied"
		case len(texts) == 0:
			msg.note = provider.Name() + " found nothing in " + from + "'s last message left for you to copy"
		}
		msg.texts, msg.ok = texts, ok
		return msg
	}
}

func (m *Model) applyJevCopy(msg jevCopyResultMsg) tea.Cmd {
	m.jevCopy.busy = false
	if !m.jevCopyOn || msg.generation != m.jevCopy.generation {
		return nil
	}
	if msg.ok || len(msg.texts) > 0 {
		m.jevCopy.turn, m.jevCopy.from, m.jevCopy.picks, m.jevCopy.next = msg.turn, msg.from, msg.texts, 0
		m.jevCopy.note = msg.note
	}
	if len(msg.texts) == 0 {
		m.errBar.text = msg.note
		return nil
	}
	logging.Info("jev copy", "from", msg.from, "picks", len(msg.texts))
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
