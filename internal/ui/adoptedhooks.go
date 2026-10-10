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

// An adopted claude never loaded the hooks a launch carries, but the global
// ones in the user's settings file run in it too, and they answer for its
// pane once the board says which row it is (hooks.SyncAdopted). This keeps
// those markers in step with the board: one for every live adopted claude,
// none for anything else, so a pane let go of, archived or taken over stops
// reporting as the row it was.

// adoptedHooksEvery bounds how stale a marker can be. A claude restarted by
// hand inside its adopted pane has a new pid, which only a fresh look finds.
const adoptedHooksEvery = 15 * time.Second

type adoptedHooksState struct {
	last time.Time
	// key is the adopted rows the last sync covered; a change in them syncs
	// at once rather than on the next tick.
	key    string
	synced bool
}

func (m *Model) adoptedClaudeRows() []adoptedClaudeRow {
	var rows []adoptedClaudeRow
	for _, sess := range m.sessions {
		if sess.TmuxPaneID == "" || sess.Archived || sess.Status == status.Dead ||
			m.cfg.Tools[sess.Tool].StatusSource != hooks.StatusSourceClaude {
			continue
		}
		rows = append(rows, adoptedClaudeRow{id: sess.ID, socket: sess.TmuxSocket, pane: sess.TmuxPaneID})
	}
	return rows
}

type adoptedClaudeRow struct{ id, socket, pane string }

// syncAdoptedHooks writes the markers when the adopted rows changed or the
// last sync is old. A board with no adopted claude clears what an earlier run
// left once and then costs nothing.
func (m *Model) syncAdoptedHooks(now time.Time) {
	if m.hooks == nil || m.tmux == nil {
		return
	}
	rows := m.adoptedClaudeRows()
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
		claude := convo.LiveClaudeSessions(convo.ClaudeHome())
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

// adoptedPane reads what the hook will see: the server pid behind $TMUX, and
// the claude whose child the hook is. A pane whose claude cannot be found gets
// no marker, and its hooks stay quiet.
func (m *Model) adoptedPane(row adoptedClaudeRow, procs *adopt.ProcTable, claude []convo.ClaudeSession) (hooks.AdoptedPane, bool) {
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
	session, ok := convo.ClaudeSessionInTree(claude, procs.PIDs(int32(panePID)))
	if !ok {
		return hooks.AdoptedPane{}, false
	}
	return hooks.AdoptedPane{ID: row.id, ServerPID: server, PaneID: row.pane, AgentPID: session.PID}, true
}
