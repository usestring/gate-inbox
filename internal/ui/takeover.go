package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// An adopted pane is on the board but not the manager's: every action that
// needs to own the pane -- steering, hooks-backed status, a kill without a
// warning, the whole MCP surface a child is born with -- refuses it. Park
// already knows how to make such a row the manager's own: read the
// conversation off the agent's process, end the pane, relaunch it as a gi_
// session on that conversation. This does the same thing while the board is
// up, one pane at a time once it is idle and no client is attached, so no
// turn or unsubmitted input is lost to it.
//
// It runs when asked: a brings one pane in (adoptoffer.go), take over in
// quick actions brings every one. With outside panes set to "take them over
// once idle", every adopted pane is owed to it the moment the board sees
// it: the idle ones go on the next pass, the busy ones on the first pass
// that finds each idle.

type takeoverState struct {
	// pending is the set the background pass is still owed: adopted rows
	// that were busy or attached when they were queued, by id.
	pending map[string]bool
	// tried is every row a pass has already attempted in this run, so a
	// pane the takeover refuses is queued once, not again on every pass.
	tried map[string]bool
}

var errTakeoverWaiting = errors.New("adopted pane is still attached to a tmux client")

func (m *Model) paneUnattended(id string) bool {
	attached, err := m.tmux.PaneState(id, "#{session_attached}")
	return err == nil && strings.TrimSpace(attached) == "0"
}

// adoptedCandidates is every live adopted agent pane on the board, idle
// rows first, then in the order the list shows them.
func (m *Model) adoptedCandidates() []store.Session {
	out := make([]store.Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		if sess.TmuxPaneID == "" || sess.Archived || sess.Status == status.Dead || m.isShell(sess.Tool) {
			continue
		}
		if _, known := m.cfg.Tools[sess.Tool]; !known {
			continue
		}
		out = append(out, sess)
	}
	sort.SliceStable(out, func(a, b int) bool {
		return takeoverReady(out[a]) && !takeoverReady(out[b])
	})
	return out
}

// takeoverReady says a pane can be restarted without losing anything: the
// agent is resting at its prompt. A working pane would lose its turn, a
// waiting one the question it is asking, a finished one the alert the
// operator has not read yet.
func takeoverReady(sess store.Session) bool {
	return sess.Status == status.Idle
}

// takeOverAdopted is take over: every adopted pane on the board now, owed
// to the takeover at once whatever the setting says, including one an
// earlier pass refused, since pressing the key is asking again.
func (m *Model) takeOverAdopted() (tea.Model, tea.Cmd) {
	candidates := m.adoptedCandidates()
	if len(candidates) == 0 {
		m.errBar.text = "no adopted panes to take over: every session on the board is already the manager's"
		return m, nil
	}
	m.errBar.text = ""
	for _, sess := range candidates {
		delete(m.takeover.tried, sess.ID)
		m.oweTakeover(sess.ID)
	}
	m.reportTakeover(m.takeoverPass())
	return m, nil
}

// oweTakeover adds one row to the set the background pass is owed.
func (m *Model) oweTakeover(id string) {
	if m.takeover.pending == nil {
		m.takeover.pending = map[string]bool{}
	}
	m.takeover.pending[id] = true
}

// autoTakeover owes the pass every adopted pane it has not tried yet, when
// outside panes are set to be taken over. Only a real
// startup does this: a Model built without Init takes nothing on its own.
func (m *Model) autoTakeover() {
	if !m.restoreArmed || m.outsidePanesMode() != paneRelaunch {
		return
	}
	for _, sess := range m.adoptedCandidates() {
		if !m.takeover.tried[sess.ID] {
			m.oweTakeover(sess.ID)
		}
	}
}

// takeoverResult is what one pass did: rows moved, rows still owed, and
// the rows it refused, each with its reason.
type takeoverResult struct {
	taken  int
	owed   int
	failed []string
}

// takeoverPass moves every pending row whose pane is idle and unattended,
// and reports what it did. The refresh retries rows still in use.
func (m *Model) takeoverPass() takeoverResult {
	var result takeoverResult
	m.autoTakeover()
	if len(m.takeover.pending) == 0 {
		return result
	}
	// A pane focused inside the board also waits for the operator to leave it.
	focused := ""
	if m.mode == modeFocus {
		if sess, ok := m.selected(); ok {
			focused = sess.ID
		}
	}
	var due []store.Session
	for _, sess := range m.sessions {
		if !m.takeover.pending[sess.ID] || sess.ID == focused {
			continue
		}
		if sess.TmuxPaneID == "" || sess.Archived || sess.Status == status.Dead {
			// Already the manager's, or gone on its own: nothing is owed.
			delete(m.takeover.pending, sess.ID)
			continue
		}
		if takeoverReady(sess) && m.paneUnattended(sess.ID) {
			due = append(due, sess)
		}
	}
	if len(due) == 0 {
		result.owed = len(m.takeover.pending)
		return result
	}
	// One read of the process table and of Claude Code's session files for
	// the whole batch: both are the same for every pane in it.
	procs := adopt.NewProcTable()
	claude := convo.LiveClaudeSessions(convo.ClaudeHome())
	if m.takeover.tried == nil {
		m.takeover.tried = map[string]bool{}
	}
	for _, sess := range due {
		if err := m.takeOver(sess, procs, claude); err != nil {
			if errors.Is(err, errTakeoverWaiting) {
				continue
			}
			delete(m.takeover.pending, sess.ID)
			m.takeover.tried[sess.ID] = true
			result.failed = append(result.failed, fmt.Sprintf("%s: %v", sess.Name, err))
			logging.Warn("takeover failed", "session", sess.ID, "name", sess.Name, logging.Err(err))
			continue
		}
		delete(m.takeover.pending, sess.ID)
		m.takeover.tried[sess.ID] = true
		result.taken++
		logging.Info("took over an adopted pane", "session", sess.ID, "name", sess.Name, "tool", sess.Tool)
	}
	result.owed = len(m.takeover.pending)
	if result.taken > 0 {
		m.requestRefresh()
	}
	return result
}

// reportTakeover puts a pass's outcome on the status bar: what moved, what
// is still owed, and the first failure by name.
func (m *Model) reportTakeover(result takeoverResult) {
	if result.taken == 0 && result.owed == 0 && len(result.failed) == 0 {
		return
	}
	var parts []string
	if result.taken > 0 {
		parts = append(parts, fmt.Sprintf("took over %d adopted %s", result.taken, plural(result.taken, "session", "sessions")))
	}
	if result.owed > 0 {
		parts = append(parts, fmt.Sprintf("%d %s until %s idle and unattended", result.owed, plural(result.owed, "waits", "wait"), plural(result.owed, "its pane is", "their panes are")))
	}
	if len(result.failed) > 0 {
		parts = append(parts, fmt.Sprintf("%d left in %s pane (%s)", len(result.failed), plural(len(result.failed), "its", "their"), result.failed[0]))
		m.errBar.text = strings.Join(parts, "; ")
		return
	}
	if result.taken > 0 && result.owed == 0 {
		parts = append(parts, "every adopted pane is the manager's now")
	}
	m.reportDone(strings.Join(parts, "; "))
}

// takeOver makes one adopted row the manager's own: the conversation is
// read off the agent's process while it is still up, the pane is ended the
// way a confirmed archive ends it, the row is promoted, and the session
// relaunched under its own id on that conversation, with the hooks and
// environment a spawned session gets. A relaunch that fails leaves the row
// dead and promoted, where v revives it: the pane is already gone, and a
// dead managed row is what park leaves too.
func (m *Model) takeOver(sess store.Session, procs *adopt.ProcTable, claude []convo.ClaudeSession) error {
	tool, known := m.cfg.Tools[sess.Tool]
	if !known {
		return fmt.Errorf("tool %s is no longer configured", sess.Tool)
	}
	if tool.Shell {
		return fmt.Errorf("%s is a shell, not an agent", sess.Name)
	}
	if !m.tmux.Exists(sess.ID) {
		return fmt.Errorf("its pane is already gone")
	}
	convID, cwd := sess.AgentSessionID, sess.Cwd
	if sess.Tool == "claude" {
		if pid, err := m.tmux.PanePID(sess.ID); err == nil && pid != 0 {
			if session, ok := convo.ClaudeSessionInTree(claude, procs.PIDs(int32(pid))); ok {
				convID = session.SessionID
				if session.Cwd != "" {
					cwd = session.Cwd
				}
			}
		}
	}
	// A tool that resumes by id needs the id: relaunching on its continue
	// command would resume the directory's most recent conversation, which
	// is the wrong one whenever panes share a checkout, and the pane would
	// already be gone. Better to leave it where it is and say so.
	if convID == "" && tool.ResumeByIDCommand != "" {
		return fmt.Errorf("no conversation id could be read off its process, left in its pane")
	}
	if !isDir(cwd) {
		return fmt.Errorf("working directory no longer exists: %s", cwd)
	}
	if err := m.endSession(sess, func(id string) error {
		if !m.paneUnattended(id) {
			return errTakeoverWaiting
		}
		return m.tmux.KillAdopted(id)
	}); err != nil {
		return err
	}
	m.tmux.Release(sess.ID)
	if err := m.store.PromoteAdopted(sess.ID, cwd, convID); err != nil {
		return err
	}
	promoted := sess
	promoted.TmuxSocket, promoted.TmuxPaneID = "", ""
	promoted.Cwd, promoted.AgentSessionID = cwd, convID
	promoted.Status = status.Dead
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i] = promoted
		}
	}
	return m.reviveSession(promoted)
}
