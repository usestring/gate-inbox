package ui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
)

// An adopted agent never loaded what a launch carries: its environment, its
// hooks, its MCP server. What can reach it anyway -- the global hooks and MCP
// relay in a claude's user settings, and the board's own CLI run from its
// shell -- answers for its pane once the board says which row it is
// (hooks.SyncAdopted). This keeps those markers in step with the board: one
// for every live adopted agent, none for anything else, so a pane let go of,
// archived or taken over stops speaking as the row it was.

// adoptedHooksEvery bounds how stale a marker can be. An agent restarted by
// hand inside its adopted pane has a new pid, which only a fresh look finds.
const adoptedHooksEvery = 15 * time.Second

type adoptedHooksState struct {
	last time.Time
	// key is the adopted rows the last sync covered; a change in them syncs
	// at once rather than on the next tick.
	key    string
	synced bool
}

// adoptedAgentRows is every live adopted row running an agent: any
// configured tool with a command that is not a shell, the same tools a scan
// adopts.
func (m *Model) adoptedAgentRows() []adoptedAgentRow {
	var rows []adoptedAgentRow
	for _, sess := range m.sessions {
		if sess.TmuxPaneID == "" || sess.Archived || sess.Status == status.Dead {
			continue
		}
		tool, ok := m.cfg.Tools[sess.Tool]
		if !ok || tool.Shell || tool.Command == "" {
			continue
		}
		rows = append(rows, adoptedAgentRow{
			id: sess.ID, socket: sess.TmuxSocket, pane: sess.TmuxPaneID,
			tool: sess.Tool, command: tool.Command,
			claude: tool.StatusSource == hooks.StatusSourceClaude,
		})
	}
	return rows
}

type adoptedAgentRow struct {
	id, socket, pane string
	// tool and command are the row's configured tool and its launch
	// command, which names the program to look for in the pane.
	tool, command string
	// claude is a hooks-driven claude, found by its session file rather
	// than its command line, whose marker the global hooks read.
	claude bool
}

// syncAdoptedHooks writes the markers when the adopted rows changed or the
// last sync is old. A board with no adopted agent clears what an earlier run
// left once and then costs nothing.
func (m *Model) syncAdoptedHooks(now time.Time) {
	if m.hooks == nil || m.tmux == nil {
		return
	}
	rows := m.adoptedAgentRows()
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, row.id+"@"+row.socket+row.pane)
	}
	slices.Sort(parts)
	key := strings.Join(parts, ",")
	state := &m.adoptedHooks
	if state.synced && key == state.key && (len(rows) == 0 || now.Sub(state.last) < adoptedHooksEvery) {
		return
	}
	state.synced, state.key, state.last = true, key, now
	var panes []hooks.AdoptedPane
	if len(rows) > 0 {
		procs := adopt.NewProcTable()
		var claude []convo.ClaudeSession
		if slices.ContainsFunc(rows, func(row adoptedAgentRow) bool { return row.claude }) {
			claude = convo.LiveClaudeSessions(convo.ClaudeHome())
		}
		for _, row := range rows {
			if pane, ok := m.adoptedPane(row, procs, claude); ok {
				panes = append(panes, pane)
			}
		}
	}
	if err := m.hooks.SyncAdopted(panes); err != nil {
		logging.Warn("adopted hook markers not synced", logging.Err(err))
	}
}

// adoptedPane reads what the marker's readers will see: the server pid
// behind $TMUX, and the agent process. A claude is the one whose session file
// names a pid in the pane's tree, the claude its hooks are children of; any
// other agent is the process in that tree whose command line runs its tool,
// the evidence adoption itself went on. A pane whose agent cannot be found
// gets no marker, and nothing speaks as its row.
func (m *Model) adoptedPane(row adoptedAgentRow, procs *adopt.ProcTable, claude []convo.ClaudeSession) (hooks.AdoptedPane, bool) {
	out, err := m.tmux.PaneState(row.id, "#{pid} #{pane_pid}")
	if err != nil {
		return hooks.AdoptedPane{}, false
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return hooks.AdoptedPane{}, false
	}
	server, err1 := strconv.Atoi(fields[0])
	panePID, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return hooks.AdoptedPane{}, false
	}
	if row.claude {
		session, ok := convo.ClaudeSessionInTree(claude, procs.PIDs(int32(panePID)))
		if !ok {
			return hooks.AdoptedPane{}, false
		}
		return hooks.AdoptedPane{ID: row.id, ServerPID: server, PaneID: row.pane, AgentPID: session.PID}, true
	}
	agent, ok := procs.ProgramPID(int32(panePID), row.command)
	if !ok {
		return hooks.AdoptedPane{}, false
	}
	return hooks.AdoptedPane{ID: row.id, ServerPID: server, PaneID: row.pane, AgentPID: agent, Tool: row.tool}, true
}
