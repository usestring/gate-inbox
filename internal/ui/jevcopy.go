package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
//
// The run key reads the same turn the same way, in the same call, but keeps
// only the one-line commands the classifier is confident the agent left for
// the operator to run, and types the next of them into the session as
// "! <command>" with Enter, which Claude Code, Codex and OpenCode take as a
// shell command run in the session. It does not wrap: once every command has
// gone out, pressing again sends nothing, so no command runs twice by accident.

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
	// commands are the picks the run key sends, and ran how many of them it
	// has sent for this turn.
	commands []string
	ran      int
}

type jevCopyResultMsg struct {
	generation uint64
	turn       string
	from       string
	texts      []string
	commands   []string
	ok         bool
	// run names the session the run key was pressed on, empty for the copy key.
	run string
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
// the reply rather than its pieces, and the commands among them the run key may
// send. ok is false when the classifier did not answer them all.
func askJevCopy(ctx context.Context, provider classifier.Provider, key string, plan *autocopy.Plan) (picks, commands []string, ok bool) {
	input := plan.Input()
	answers, ok := askJevNoul(ctx, provider, jevHTTPClient, provider.Endpoint(), key,
		map[string]any{"message": input.Message}, input.Questions)
	if !ok {
		return nil, nil, false
	}
	picks, err := plan.Select(answers)
	if err != nil {
		return nil, nil, false
	}
	return picks, runnableCommands(input.Questions, answers), true
}

// jevRunQuestion opens autocopy's per-candidate command question ("c1", "c2",
// ...), which quotes the candidate after it. autocopy does not export which
// candidate a question asks about, so the run key reads it back from here; a
// test pins the wording so an autocopy upgrade that changes it fails loudly
// instead of leaving the key with nothing to run.
const jevRunQuestion = "Is this text a command supplied for the user to run? Text: "

// runnableCommands are the candidates the classifier answered the command
// question for at the copy threshold, in the order the agent wrote them. A
// command is held to the stricter copy threshold rather than autocopy's
// command one because this key runs it rather than copying it. A command
// spanning lines is left out, since the prompt would take its first newline
// as Enter, and a leading "!" the agent already wrote is dropped so the key
// does not send "! ! cmd".
func runnableCommands(questions map[string]string, answers map[string]float64) []string {
	type numbered struct {
		index int
		text  string
	}
	var found []numbered
	for name, question := range questions {
		index, err := strconv.Atoi(strings.TrimPrefix(name, "c"))
		if !strings.HasPrefix(name, "c") || err != nil || !strings.HasPrefix(question, jevRunQuestion) {
			continue
		}
		if answers[name] < autocopy.Threshold {
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(question, jevRunQuestion)), "!"))
		if text == "" || strings.ContainsAny(text, "\r\n") {
			continue
		}
		found = append(found, numbered{index, text})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].index < found[j].index })
	commands := make([]string, len(found))
	for i, command := range found {
		commands[i] = command.text
	}
	return commands
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

// runSelectedWithJev runs the run key on the list's selected row.
func (m *Model) runSelectedWithJev() (tea.Model, tea.Cmd) {
	sess, ok := m.selected()
	if !ok || sess.Archived {
		return m, nil
	}
	return m.runWithJev(sess)
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
	return m.readJevTurn(sess, turn, "")
}

// runWithJev types the next command from sess's latest turn into sess, first
// reading that turn the way the copy key does when what is on hand came from
// another one.
func (m *Model) runWithJev(sess store.Session) (tea.Model, tea.Cmd) {
	if !m.jevCopyOn || m.jevCopy.busy {
		return m, nil
	}
	turn := jevCopyIdentity(sess)
	if turn == m.jevCopy.turn {
		return m, m.runNextCommand(sess)
	}
	return m.readJevTurn(sess, turn, sess.ID)
}

// readJevTurn reads sess's latest turn and asks the classifier about it off the
// event loop. run is the session to send the first command to once it lands,
// or empty to copy the first pick instead.
func (m *Model) readJevTurn(sess store.Session, turn, run string) (tea.Model, tea.Cmd) {
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
		msg := jevCopyResultMsg{generation: generation, turn: turn, from: from, run: run}
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
		texts, commands, ok := askJevCopy(ctx, provider, key, plan)
		switch {
		case !ok:
			msg.note = provider.Name() + " did not answer; nothing copied"
		case len(texts) == 0:
			msg.note = provider.Name() + " found nothing in " + from + "'s last message left for you to copy"
		}
		msg.texts, msg.commands, msg.ok = texts, commands, ok
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
		m.jevCopy.commands, m.jevCopy.ran = msg.commands, 0
		m.jevCopy.note = msg.note
	}
	if msg.run != "" {
		sess, ok := m.sessionByID(msg.run)
		if !ok || jevCopyIdentity(sess) != msg.turn {
			// The row went away or moved on while the classifier read it:
			// what it found is for a turn the operator is no longer looking at.
			m.errBar.text = msg.from + " moved on while it was read; nothing sent"
			return nil
		}
		if !msg.ok && len(msg.texts) == 0 {
			m.errBar.text = msg.note
			return nil
		}
		return m.runNextCommand(sess)
	}
	if len(msg.texts) == 0 {
		m.errBar.text = msg.note
		return nil
	}
	logging.Info("jev copy", "from", msg.from, "picks", len(msg.texts))
	return m.copyNextPick()
}

// runNextCommand types the next command the classifier found in sess's
// latest turn into sess, with Enter. Unlike the copy key it stops after the
// last rather than wrapping, so pressing again never runs a command twice.
func (m *Model) runNextCommand(sess store.Session) tea.Cmd {
	from := m.jevCopy.from
	switch {
	case len(m.jevCopy.commands) == 0:
		m.errBar.text = "nothing in " + from + "'s last message left for you to run"
		return nil
	case m.jevCopy.ran >= len(m.jevCopy.commands):
		m.errBar.text = fmt.Sprintf("already sent all %d commands from %s's last message", len(m.jevCopy.commands), from)
		return nil
	}
	index := m.jevCopy.ran
	command := m.jevCopy.commands[index]
	quoted := `"! ` + command + `"`
	if len(m.jevCopy.commands) > 1 {
		quoted += fmt.Sprintf(" (%d of %d)", index+1, len(m.jevCopy.commands))
	}
	cmd, sent := sendJevCommand(m, sess, "! "+command, quoted)
	if sent {
		// Only a command that went out is spent; a refused one is the
		// next press's to send once the pane can take it.
		m.jevCopy.ran++
		logging.Info("jev run", "from", from, "index", index+1, "total", len(m.jevCopy.commands))
	}
	return cmd
}

// sendJevCommand types a command line into sess and presses Enter through
// the send a snippet takes, and reports whether it went out. It refuses
// before that send can do anything else with the line: a parked row would be
// revived to take it, and a question dialog would take it as the answer.
// Tests swap it to see what would have been typed.
var sendJevCommand = func(m *Model, sess store.Session, text, quoted string) (tea.Cmd, bool) {
	switch {
	case sess.Archived:
		m.errBar.text = m.displayName(sess) + " is archived — " + m.pressTo(keymap.ContextList, keymap.Restore, "restore it first")
		return nil, false
	case m.isShell(sess.Tool):
		m.errBar.text = shellPromptHint(sess.Name)
		return nil, false
	case !m.tmux.Exists(sess.ID):
		m.errBar.text = m.deadSessionHint()
		return nil, false
	}
	if hold, _ := m.dialogHold(sess); hold != "" {
		m.errBar.text = hold
		return nil, false
	}
	return m.sendSentence(sess, text, quoted, true, false), true
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
