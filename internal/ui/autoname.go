package ui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/opencode"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/sessname"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// autoNameInterval is how often the board looks for a better name than the one
// it made up. Slower than the poll on purpose: a session's title is written
// once and then sits there, and a rail whose names move while somebody is
// reading it is worse than one that is a minute behind.
const autoNameInterval = 30 * time.Second

// autoNameScrollback is how much of a pane is compared against a conversation's
// own words. The visible screen alone is too little -- an agent that just
// printed a tool result has pushed its prose off the top.
const autoNameScrollback = 200

type autoNameTickMsg struct{}

// renamedSession is one row a naming pass renamed.
type renamedSession struct {
	id   string
	name string
}

// autoNamedMsg carries what a naming pass changed.
//
// It carries the names themselves rather than a count on purpose. The pass
// writes to sqlite from a worker goroutine, and the rail draws from m.sessions
// on the event loop; asking the poller to go and re-read the rows it just
// wrote leaves the two out of step for however long that takes, and if the
// refresh never arrives the rename is invisible until the manager is
// restarted. Handing the names back means Update can put them on the rail
// itself, on the loop, on the very next frame -- and it stays the only place
// that touches m.sessions.
type autoNamedMsg struct {
	targets map[string]search.Target
	renamed []renamedSession
	// prompts is the opening prompts of every session the pass could
	// attribute a conversation to, by session id.
	prompts map[string][]string
	err     error
}

func (m *Model) autoNameTick() tea.Cmd {
	return tea.Tick(autoNameInterval, func(time.Time) tea.Msg { return autoNameTickMsg{} })
}

// autoNamePane is one session's pane, snapshotted on the event loop.
//
// socket and paneID are an adopted row's address, and are empty for a row the
// manager launched: a managed session is reached by the conversation id it was
// launched with instead, which needs no tmux at all.
type autoNamePane struct {
	sessID  string
	name    string
	tool    string
	cwd     string
	socket  string
	paneID  string
	agentID string
	// nameable is a row wearing a name the manager made up, which the pass
	// may replace. A row somebody named rides the sweep only for its
	// opening prompts.
	nameable bool
}

// autoNameScan replaces the names the manager made up with the ones the agents
// already wrote for themselves.
//
// Everything the pass reads out of the model is read here, on the event loop,
// and the closure below gets a copy. Reading the model from inside a command is
// a data race -- commands run on their own goroutine while Update writes -- and
// this package has been bitten by exactly that before.
func (m *Model) autoNameScan() tea.Cmd {
	if m.store == nil || m.convos == nil || m.autoNaming {
		return nil
	}
	var panes []autoNamePane
	// taken is every name on the board, the candidates' own included. A row
	// this pass cannot name -- no conversation could be attributed to it --
	// keeps the name it has, so that name must not be handed to another row.
	taken := map[string]bool{}
	for _, sess := range m.sessions {
		taken[sess.Name] = true
		if sess.Archived {
			continue
		}
		// A name a person or an agent asked for is not ours to replace, so
		// the row only rides the sweep while its opening prompts are still
		// short of the count: once they are in, there is no reason to spend
		// a pane capture on it.
		nameable := sess.NameSource == store.SourceDerived || sess.NameSource == store.SourceTitle
		if !nameable && (len(m.firstPrompts[sess.ID]) >= firstPromptRows || !m.openingReadable(sess)) {
			continue
		}
		// An adopted row is found through the pane it was discovered in. A row
		// the manager launched has no pane recorded and never will -- that
		// column is adoption's -- so it is carried by the conversation id it
		// launched with, and a row with neither is unreachable.
		if sess.TmuxPaneID == "" && sess.AgentSessionID == "" {
			continue
		}
		panes = append(panes, autoNamePane{
			sessID: sess.ID, name: sess.Name, tool: sess.Tool, cwd: sess.Cwd,
			socket: sess.TmuxSocket, paneID: sess.TmuxPaneID, agentID: sess.AgentSessionID,
			nameable: nameable,
		})
	}
	if len(panes) == 0 {
		return nil
	}
	m.autoNaming = true
	index, drift, stor, driver := m.convos, m.drift, m.store, m.tmux

	return func() tea.Msg {
		started := time.Now()
		linkPanes := make([]convo.Pane, 0, len(panes))
		dirs := make([]string, 0, len(panes))
		seenDir := map[string]bool{}
		panePID := panePIDs(panes)
		// One snapshot for the whole sweep: a table per pane is thirty full
		// /proc scans a tick on a board this size.
		procs := adopt.NewProcTable()
		captures := sweepCaptures(driver, panes)
		for _, pane := range panes {
			entry := convo.Pane{
				Key:     pane.sessID,
				Tool:    pane.tool,
				Cwd:     pane.cwd,
				AgentID: pane.agentID,
			}
			if pane.paneID != "" {
				capture, ok := captures[adoptKey(pane.socket, pane.paneID)]
				if !ok || capture.Err != nil {
					continue
				}
				entry.Text = convo.Normalize(capture.Text)
				entry.PIDs = procs.PIDs(panePID[adoptKey(pane.socket, pane.paneID)])
			}
			linkPanes = append(linkPanes, entry)
			if !seenDir[pane.cwd] {
				seenDir[pane.cwd] = true
				dirs = append(dirs, pane.cwd)
			}
		}
		scanned := time.Now()
		// A refresh that failed partway still published what it read, and one
		// tool being unreadable is no reason to leave every pane on the board
		// unnamed. The failure is reported once the naming it did not stop has
		// finished.
		refreshErr := index.Refresh(dirs)
		refreshed := time.Now()
		assigned := convo.Link(linkPanes, index.Conversations())
		linked := time.Now()

		live := make([]string, 0, len(panes))
		for _, pane := range panes {
			live = append(live, pane.sessID)
		}
		drift.Keep(live)

		entries := make([]sessname.Entry, 0, len(assigned.Matched))
		prompts := map[string][]string{}
		targets := map[string]search.Target{}
		for _, pane := range panes {
			match, ok := assigned.For(pane.sessID)
			if !ok {
				continue
			}
			targets[pane.sessID] = search.Target{Key: pane.sessID, Tool: match.Conversation.Tool, AgentID: match.Conversation.ID, Path: match.Conversation.TranscriptPath}
			if opening := typedPrompts(match.Conversation.FirstPrompts); len(opening) > 0 {
				prompts[pane.sessID] = opening
			}
			if !pane.nameable {
				continue
			}
			title := drift.Title(pane.sessID, match.Conversation.Title, match.Conversation.Prompts)
			if title == "" {
				continue
			}
			entries = append(entries, sessname.Entry{
				ID:       pane.sessID,
				Title:    title,
				Current:  pane.name,
				Fallback: filepath.Base(pane.cwd),
			})
		}
		names := sessname.Assign(sessname.Kebab{}, entries, taken)

		var renamed []renamedSession
		for _, entry := range entries {
			name := names[entry.ID]
			if name == "" || name == entry.Current {
				continue
			}
			// Only a row the store actually took is reported: the guard can
			// refuse one the user renamed since the snapshot, and putting that
			// name on the rail anyway would show something the store disagrees
			// with until the next poll corrected it.
			ok, err := stor.AutoRenameSession(entry.ID, name, store.SourceTitle)
			if err != nil {
				return autoNamedMsg{renamed: renamed, err: err}
			}
			if ok {
				renamed = append(renamed, renamedSession{id: entry.ID, name: name})
			}
		}
		logSweep(started, scanned, refreshed, linked, index.Cost(), len(panes), len(renamed))
		return autoNamedMsg{renamed: renamed, prompts: prompts, targets: targets, err: refreshErr}
	}
}

// slowSweep is when a naming sweep stops being routine. It runs beside the
// poll loop, so a slow one reaches the operator as a board that stopped
// updating -- which the log has to be able to tell from a slow pass.
const slowSweep = time.Second

func logSweep(started, scanned, refreshed, linked time.Time, cost convo.Cost, panes, renamed int) {
	elapsed := time.Since(started)
	level := logging.LevelDebug
	if elapsed > slowSweep {
		level = logging.LevelInfo
	}
	if !logging.Enabled(level) {
		return
	}
	// refresh is split out from link, and broken down further, because "link"
	// covered both the transcript I/O and the pure matching that follows it,
	// and a sweep that got slower gave no way to tell which had grown.
	fields := []any{
		"panes", panes,
		"renamed", renamed,
		"took", elapsed.Round(time.Millisecond).String(),
		"scan", scanned.Sub(started).Round(time.Millisecond).String(),
		"refresh", refreshed.Sub(scanned).Round(time.Millisecond).String(),
		"refresh.sidecars", cost.Sidecars.Round(time.Millisecond).String(),
		"refresh.paths", cost.Paths.Round(time.Millisecond).String(),
		"refresh.tails", cost.Tails.Round(time.Millisecond).String(),
		"refresh.opencode", cost.OpenCode.Round(time.Millisecond).String(),
		"refresh.files", cost.Files,
		"refresh.heads", cost.Heads,
		"refresh.cached", cost.Cached,
		"link", linked.Sub(refreshed).Round(time.Millisecond).String(),
		"rename", time.Since(linked).Round(time.Millisecond).String(),
	}
	if level == logging.LevelInfo {
		logging.Info("naming sweep", fields...)
		return
	}
	logging.Debug("naming sweep", fields...)
}

// panePIDs is each pane's process, which is where the walk for the agent's own
// pid starts. One listing per tmux server rather than one per pane: a board
// this size is eighty-odd panes across two or three servers, and asking each
// one separately is eighty tmux processes a tick.
func panePIDs(panes []autoNamePane) map[string]int32 {
	sockets := map[string]bool{}
	for _, pane := range panes {
		// A managed row addresses no pane, and its empty socket would list the
		// default server for nothing.
		if pane.paneID == "" {
			continue
		}
		sockets[pane.socket] = true
	}
	if len(sockets) == 0 {
		return nil
	}
	out := map[string]int32{}
	for socket := range sockets {
		for _, candidate := range adopt.Panes(socket) {
			out[adoptKey(candidate.Socket, candidate.PaneID)] = candidate.PID
			// A row can hold the default server as "" while tmux names it,
			// so the pane is filed under both spellings of the same socket.
			if candidate.Socket == adopt.DefaultSocket {
				out[adoptKey("", candidate.PaneID)] = candidate.PID
			}
		}
	}
	return out
}

// newConvoIndex points the index at the agents' own state.
//
// CLAUDE_CONFIG_DIR is honoured because Claude Code honours it: an operator who
// has moved their configuration has moved their transcripts with it, and
// reading the default path would quietly find nothing. A location that does not
// exist simply contributes no conversations.
func newConvoIndex() *convo.Index {
	claude := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	home, err := os.UserHomeDir()
	if err != nil && claude == "" {
		return convo.New("", "")
	}
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	// Resolved the way opencode resolves it, so XDG_DATA_HOME and the
	// OPENCODE_DB overrides are honoured here exactly as in history
	// search. A location that does not exist simply contributes no
	// conversations.
	return convo.New(claude, opencode.DBPath())
}

// applyRenames puts the names a pass chose onto the rows the rail draws from,
// and reports whether anything moved.
//
// This runs on the event loop, from Update, which is the only place m.sessions
// is written. The pass that produced these names never touched it: a command
// runs on its own goroutine while Update writes, and reading the model from
// one is a data race this package has already paid for once.
func (m *Model) applyRenames(renamed []renamedSession) bool {
	if len(renamed) == 0 {
		return false
	}
	byID := make(map[string]string, len(renamed))
	for _, row := range renamed {
		byID[row.id] = row.name
	}
	moved := false
	for i := range m.sessions {
		name, ok := byID[m.sessions[i].ID]
		if !ok || m.sessions[i].Name == name {
			continue
		}
		m.sessions[i].Name = name
		// The row is no longer wearing a name the manager made up, and the
		// next pass has to see that or it will offer the name again.
		m.sessions[i].NameSource = store.SourceTitle
		// The pane's own status bar carries the name too, so a row renamed
		// while its bar still reads the old one is one session calling itself
		// two things on one screen. An adopted pane refuses the write, which
		// is right: its status bar was never the manager's to set.
		if m.tmux != nil {
			_ = m.tmux.SetLabel(m.sessions[i].ID, sessionLabel(m.sessions[i].Group, name))
		}
		moved = true
	}
	return moved
}

// openingReadable reports whether the conversation index can read a
// session's opening at all: only the Claude transcript reader keeps one, so
// a named row on any other tool has nothing to ride the sweep for.
func (m *Model) openingReadable(sess store.Session) bool {
	return historyToolFormats(m.cfg)[sess.Tool] == search.ToolClaude
}

// typedPrompts is a conversation's opening prompts as they were typed: the
// notes a launch puts ahead of the first one come off, and a first record
// that was nothing but notes drops out.
func typedPrompts(opening []string) []string {
	out := make([]string, 0, len(opening))
	for _, prompt := range opening {
		if typed := launch.TypedPrompt(prompt); typed != "" {
			out = append(out, typed)
		}
	}
	return out
}

// applyFirstPrompts records what a pass learned about the sessions'
// openings. It runs on the event loop, like applyRenames. A shorter answer
// never replaces a longer one: the pass reads a bounded head, and a session
// with three prompts on file has nothing left to learn.
func (m *Model) applyFirstPrompts(prompts map[string][]string) {
	if m.firstPrompts == nil && len(prompts) > 0 {
		m.firstPrompts = map[string][]string{}
	}
	for id, opening := range prompts {
		if len(opening) > len(m.firstPrompts[id]) {
			m.firstPrompts[id] = opening
		}
	}
}

// sweepCaptures reads every pane the sweep needs, batched per tmux server over
// the driver's pooled control client. The sweep used to fork one tmux per pane,
// which on a 29-pane board was 76ms of its own -- the largest phase left in it
// once the shared ProcTable landed.
//
// Keyed by adoptKey rather than pane id: a board spans several servers and two
// of them can both have a "%1".
func sweepCaptures(driver *tmux.Driver, panes []autoNamePane) map[string]tmux.Capture {
	bySocket := map[string][]string{}
	for _, pane := range panes {
		if pane.paneID == "" {
			continue
		}
		bySocket[pane.socket] = append(bySocket[pane.socket], pane.paneID)
	}
	out := make(map[string]tmux.Capture, len(panes))
	for socket, ids := range bySocket {
		for paneID, capture := range driver.CaptureScrollback(socket, ids, autoNameScrollback) {
			out[adoptKey(socket, paneID)] = capture
		}
	}
	return out
}
