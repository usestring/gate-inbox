package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// Reopening the board after a while away can find two things: the board's own
// sessions stopped, and agent panes somebody started by hand in the meantime.
// The first are marked on the list (diedwhileclosed.go), never asked about.
// The second needs no question: the adopt scan puts such a pane on the board,
// and the takeover (takeover.go) makes it a board session once it is idle, so
// it gets the hooks, the MCP surface and a title from the naming sweep. The settings
// here let an operator keep panes as they are, or leave them off the board.

// Settings keys and their values. "mark" is the default for reopen_sessions;
// outside_panes defaults to relaunching, which is the takeover.
const (
	reopenSessionsSetting = "reopen_sessions"
	outsidePanesSetting   = "outside_panes"
	// outsidePanesDecidedSetting remembers the panes left off the board, so
	// the scan does not take them again. See paneDecisions.
	outsidePanesDecidedSetting = "outside_panes_decided"

	reopenMark   = "mark"
	reopenResume = "resume"
	reopenNever  = "never"

	paneAdopt    = "adopt"
	paneRelaunch = "relaunch"
	paneIgnore   = "ignore"
)

var (
	reopenSessionsModes = []string{reopenMark, reopenResume, reopenNever}
	outsidePanesModes   = []string{paneRelaunch, paneAdopt, paneIgnore}
)

// normalizeReopenSessions reads a stored "ask", from before the board stopped
// asking at startup, as the default.
func normalizeReopenSessions(mode string) string {
	for _, known := range reopenSessionsModes {
		if mode == known {
			return mode
		}
	}
	return reopenMark
}

// normalizeOutsidePanes reads a stored "ask", from before the card stopped
// asking about panes, as the default.
func normalizeOutsidePanes(mode string) string {
	for _, known := range outsidePanesModes {
		if mode == known {
			return mode
		}
	}
	return paneRelaunch
}

// reopenSessionsLabel and outsidePanesLabel are the settings rows' words.
func reopenSessionsLabel(mode string) string {
	switch normalizeReopenSessions(mode) {
	case reopenResume:
		return "always resume the ones that died"
	case reopenNever:
		return "leave them unmarked"
	}
	return "mark them in the list"
}

func outsidePanesLabel(mode string) string {
	switch normalizeOutsidePanes(mode) {
	case paneAdopt:
		return "keep them as-is"
	case paneIgnore:
		return "ignore them"
	}
	return "take them over once idle"
}

func (m *Model) storedMode(key string, normalize func(string) string) string {
	if m.store == nil {
		return normalize("")
	}
	value, err := m.store.Setting(key)
	if err != nil {
		logging.Info("setting unreadable", "key", key, logging.Err(err))
	}
	return normalize(value)
}

func (m *Model) reopenSessionsMode() string {
	return m.storedMode(reopenSessionsSetting, normalizeReopenSessions)
}

func (m *Model) outsidePanesMode() string {
	return m.storedMode(outsidePanesSetting, normalizeOutsidePanes)
}

// paneDecisions is the ledger of panes left off the board. Such a pane has no
// row, so its answer is keyed by where it is and the process it runs
// (paneDecisionKey). The process is in the key because tmux numbers panes
// afresh when its server restarts, and a pane id alone would carry a "leave
// this out" onto a stranger. Keys without a "|" are per-row answers from when
// the reopen card asked about panes; nothing reads them any more.
type paneDecisions map[string]string

func paneDecisionKey(socket, paneID string, pid int) string {
	return fmt.Sprintf("%s|%s|%d", socket, paneID, pid)
}

func loadPaneDecisions(st *store.Store) paneDecisions {
	decided := paneDecisions{}
	if st == nil {
		return decided
	}
	raw, err := st.Setting(outsidePanesDecidedSetting)
	if err != nil || raw == "" {
		return decided
	}
	if err := json.Unmarshal([]byte(raw), &decided); err != nil {
		logging.Info("outside pane decisions unreadable", logging.Err(err))
		return paneDecisions{}
	}
	return decided
}

func savePaneDecisions(st *store.Store, decided paneDecisions) error {
	if st == nil {
		return nil
	}
	if len(decided) == 0 {
		return st.SetSetting(outsidePanesDecidedSetting, "")
	}
	raw, err := json.Marshal(decided)
	if err != nil {
		return err
	}
	return st.SetSetting(outsidePanesDecidedSetting, string(raw))
}

// ignoredPaneKeys is the part of the ledger the adopt scan reads: panes it
// must not take again.
func (d paneDecisions) ignoredPaneKeys() map[string]bool {
	keys := map[string]bool{}
	for key, choice := range d {
		if choice == paneIgnore && strings.Contains(key, "|") {
			keys[key] = true
		}
	}
	return keys
}

// leaveOutPanes takes panes off the board without touching them and keeps
// the scan from taking them again, which is what "ignore them" means for a
// pane that was already adopted. It reports how many it left out.
func (m *Model) leaveOutPanes(panes []store.Session) int {
	if len(panes) == 0 {
		return 0
	}
	decided := loadPaneDecisions(m.store)
	for _, sess := range panes {
		pid := 0
		if m.tmux != nil {
			pid, _ = m.tmux.PanePID(sess.ID)
		}
		decided[paneDecisionKey(sess.TmuxSocket, sess.TmuxPaneID, pid)] = paneIgnore
		delete(decided, sess.ID)
	}
	if err := savePaneDecisions(m.store, decided); err != nil {
		m.errBar.text = "saving the panes left out: " + err.Error()
	}
	m.forgetOutsidePanes(panes)
	return len(panes)
}

// forgetOutsidePanes takes rows off the board and leaves their panes running,
// the way the poller forgets a pane that closed.
func (m *Model) forgetOutsidePanes(panes []store.Session) {
	if len(panes) == 0 {
		return
	}
	ids := make(map[string]bool, len(panes))
	for _, sess := range panes {
		ids[sess.ID] = true
		if m.poller != nil {
			if err := m.poller.forgetAdopted(sess.ID); err != nil {
				m.errBar.text = "leaving out " + sess.Name + ": " + err.Error()
			}
		} else if m.store != nil {
			if err := ignoreDeletedSession(m.store.Delete(sess.ID)); err != nil {
				m.errBar.text = "leaving out " + sess.Name + ": " + err.Error()
			}
		}
	}
	kept := m.sessions[:0]
	for _, sess := range m.sessions {
		if !ids[sess.ID] {
			kept = append(kept, sess)
		}
	}
	m.sessions = kept
	m.rebuildRows()
}
