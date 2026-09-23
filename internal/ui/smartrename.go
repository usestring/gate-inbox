package ui

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/sessname"
	"github.com/usestring/gate-inbox/internal/store"
)

// The smart rename is r: the row under the cursor takes the name its own
// conversation already carries. It is the same attribution the periodic pass
// makes, aimed at one row and run because somebody asked rather than because a
// ticker fired, which is what lets it do two things the pass may not -- replace
// a name a person chose, and answer at once instead of within half a minute.
//
// It is deliberately not the name sweep on N. The sweep types a directive into
// panes it did not start and asks the agents in them to rename themselves; it
// is bulk, it is slow, and its whole design is a dry run and two gates. Pressing
// a key on one row should not do any of that. This reads files the agents have
// already written and touches no pane.
//
// Nearly every pane on this board shares one working directory, so a cwd
// lookup would refuse the rows the key is actually pressed on; internal/convo's
// package comment has the measurement and the rule that follows from it. That
// is why the linking goes through convo.Link, which separates same-directory
// panes on the agent's own process -- Claude Code writes
// ~/.claude/sessions/<pid>.json naming the pid and the conversation, and a pid
// in this pane's process tree is an identity rather than a resemblance. A
// managed row needs none of that: it is carried by the conversation id it was
// launched with.

// smartRenamedMsg is what one single-row naming attempt concluded.
//
// reason carries a refusal the operator should read rather than an error: a
// pane nothing could be attributed to is a normal outcome and not a failure,
// and reporting it as one trains people to ignore the bar.
type smartRenamedMsg struct {
	sessID string
	name   string
	reason string
	err    error
}

// smartRenameSelected names the row under the cursor after the conversation
// running in it.
//
// A group row has no conversation, so r opens the group card there instead --
// editing it is what r already meant for a group, and the only thing it could.
//
// Everything the attempt reads out of the model is read here, on the event
// loop, and the closure below gets a copy. Reading the model from inside a
// command is a data race: commands run on their own goroutine while Update
// writes, and this package has been bitten by exactly that before.
func (m *Model) smartRenameSelected() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if entry.isGroup {
		m.editGroupRow(entry)
		return m, nil
	}
	sess := entry.sess
	// Ask the agent before reconstructing an answer from the outside. A tool
	// with a rename command names its own session from inside the conversation
	// it is having, and answers by running `gate-inbox rename`, which the
	// poller picks up like any other self-chosen name.
	if command := m.renameCommandFor(sess); command != "" {
		if err := m.tmux.SendText(sess.ID, command); err != nil {
			m.errBar.text = "rename: " + err.Error()
			return m, nil
		}
		m.reportDone("asked " + sess.Name + " to name itself")
		return m, nil
	}
	if m.store == nil || m.convos == nil {
		return m, nil
	}
	// A row addressing neither a pane nor a conversation is unreachable: there
	// is nothing to capture and nothing to look the conversation up by.
	if sess.TmuxPaneID == "" && sess.AgentSessionID == "" {
		m.errBar.text = "no pane or conversation to read a name from"
		return m, nil
	}
	if m.smartNaming {
		return m, nil
	}
	pane := autoNamePane{
		sessID: sess.ID, name: sess.Name, tool: sess.Tool, cwd: sess.Cwd,
		socket: sess.TmuxSocket, paneID: sess.TmuxPaneID, agentID: sess.AgentSessionID,
	}
	// taken is every other name on the board, so the row cannot be handed one
	// already in use. Its own name is left out: keeping it would make the row
	// collide with itself and take a suffix it does not need.
	taken := map[string]bool{}
	for _, other := range m.sessions {
		if other.ID != sess.ID {
			taken[other.Name] = true
		}
	}
	m.smartNaming = true
	m.errBar.text = ""
	index, drift, stor := m.convos, m.drift, m.store
	return m, func() tea.Msg { return smartRename(pane, taken, index, drift, stor) }
}

// renameCommandFor is what to type into this session's pane to have its agent
// name itself, or "" when nothing can be asked and the name has to be derived.
//
// Every agent is asked, not only the ones with a slash command. A tool that has
// somewhere to install commands gets the short form (`/rename` for claude and
// codex, which the repo links into both); every other tool gets the same
// request in prose, which is all the sweep and the spawn directive have ever
// needed. Being able to read a sentence and run one shell command is the whole
// requirement, and an agent CLI that cannot do that has no business on a board.
//
// An adopted pane is excluded rather than unsupported: it carries neither the
// manager's config directory nor its session id, so the rename subcommand run
// inside it has nothing to write to. The name sweep on N is the path for those,
// and it spells both values out in the line it types.
func (m *Model) renameCommandFor(sess store.Session) string {
	if m.isShell(sess.Tool) || sess.Tool == "" {
		return ""
	}
	if sess.TmuxPaneID != "" || m.tmux == nil || !m.tmux.Exists(sess.ID) {
		return ""
	}
	if command := m.cfg.Tools[sess.Tool].RenameCommand; command != "" {
		return command
	}
	return launch.DeferredRenameDirective
}

// smartRename is the attempt itself, off the event loop.
//
// It captures a pane, refreshes the conversation index and writes to sqlite,
// any of which can take seconds on a loaded board. None of it may happen in a
// key handler: the store keeps one connection and a poll pass occupies it for
// as long as its own reads take, so a write issued from Update is felt as a key
// that did nothing.
func smartRename(pane autoNamePane, taken map[string]bool, index *convo.Index, drift *sessname.Drift, stor *store.Store) tea.Msg {
	entry := convo.Pane{Key: pane.sessID, Tool: pane.tool, Cwd: pane.cwd, AgentID: pane.agentID}
	if pane.paneID != "" {
		text, err := adopt.CaptureLines(pane.socket, pane.paneID, autoNameScrollback)
		if err != nil {
			return smartRenamedMsg{sessID: pane.sessID, err: err}
		}
		entry.Text = convo.Normalize(text)
		entry.PIDs = adopt.NewProcTable().PIDs(panePIDs([]autoNamePane{pane})[adoptKey(pane.socket, pane.paneID)])
	}
	// A refresh that failed partway still published what it read, so the link
	// is attempted either way and the failure is reported behind whatever it
	// managed to conclude.
	refreshErr := index.Refresh([]string{pane.cwd})
	match, ok := convo.Link([]convo.Pane{entry}, index.Conversations()).For(pane.sessID)
	if !ok {
		return smartRenamedMsg{sessID: pane.sessID, reason: unresolvedReason(entry), err: refreshErr}
	}
	// TitleNow rather than Title: the periodic pass waits for a drifted title
	// to be derived twice before it believes it, and a press of the key is the
	// operator saying they want the current answer now rather than on the pass
	// after next.
	title := drift.TitleNow(pane.sessID, match.Conversation.Title, match.Conversation.Prompts)
	if title == "" {
		return smartRenamedMsg{
			sessID: pane.sessID,
			reason: "its conversation has no title yet",
			err:    refreshErr,
		}
	}
	names := sessname.Assign(sessname.Kebab{}, []sessname.Entry{{
		ID:       pane.sessID,
		Title:    title,
		Current:  pane.name,
		Fallback: filepath.Base(pane.cwd),
	}}, taken)
	name := names[pane.sessID]
	if name == "" {
		return smartRenamedMsg{
			sessID: pane.sessID,
			reason: "its conversation gives no usable name",
			err:    refreshErr,
		}
	}
	// The name is written through even when it is the one the row already wore.
	// Reporting that as nothing to do reads as the key having failed, and it
	// leaves the row recorded under whoever named it last rather than under its
	// own conversation.
	//
	// RenameSessionAs rather than AutoRenameSession: the automatic path refuses
	// a name a person chose, and replacing exactly that is what pressing the key
	// asks for. The row is recorded as title-named so the periodic pass keeps it
	// current afterwards instead of treating it as hands-off.
	if err := stor.RenameSessionAs(pane.sessID, name, store.SourceTitle); err != nil {
		return smartRenamedMsg{sessID: pane.sessID, err: err}
	}
	return smartRenamedMsg{sessID: pane.sessID, name: name, err: refreshErr}
}

// unresolvedReason says which route to a conversation was missing, rather than
// only that there was none. On a board where every pane shares one directory
// the difference between "no agent process under this pane" and "nothing
// matched" is the difference between a stale sidecar and a wrong tool, and the
// operator can act on the first.
func unresolvedReason(pane convo.Pane) string {
	switch {
	case pane.Tool == "":
		return "this row has no tool, so nothing links it to a conversation"
	case pane.AgentID == "" && len(pane.PIDs) == 0:
		return "no agent process under this pane to identify its conversation"
	default:
		return "no conversation could be attributed to this pane"
	}
}

// applySmartRename puts a finished attempt on the board, on the event loop.
func (m *Model) applySmartRename(msg smartRenamedMsg) (tea.Model, tea.Cmd) {
	m.smartNaming = false
	switch {
	case msg.err != nil:
		m.errBar.text = "smart rename: " + msg.err.Error()
	case msg.reason != "":
		m.errBar.text = "smart rename: " + msg.reason
	}
	if msg.name == "" {
		return m, nil
	}
	// applyRenames reports whether the rail moved, which a press landing on the
	// name the row already had does not. The store still took the write, so the
	// report and the refresh are not the rail's to withhold.
	if m.applyRenames([]renamedSession{{id: msg.sessID, name: msg.name}}) {
		m.rebuildRows()
	} else {
		m.markTitleNamed(msg.sessID)
	}
	if msg.err == nil {
		m.reportDone("renamed to " + msg.name)
	}
	// The rail is already right; this is for everything else the board
	// derives from a session row.
	m.poller.requestRefresh()
	return m, nil
}

// markTitleNamed records on the rail what the store was just told: the row
// wears a name derived from its own conversation, and the periodic pass may
// keep it current. applyRenames does this for a row whose name changed; this is
// the same row when the name it was given is the one it already had.
func (m *Model) markTitleNamed(id string) {
	for i := range m.sessions {
		if m.sessions[i].ID == id {
			m.sessions[i].NameSource = store.SourceTitle
			return
		}
	}
}
