package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// Settings keys and their values. Outside panes are ignored by default.
const (
	reopenSessionsSetting = "reopen_sessions"
	outsidePanesSetting   = "outside_panes"
	// outsidePanesDecidedSetting remembers panes explicitly kept or left out.
	outsidePanesDecidedSetting = "outside_panes_decided"

	reopenAsk    = "ask"
	reopenResume = "resume"
	reopenNever  = "never"

	paneAdopt    = "adopt"
	paneRelaunch = "relaunch"
	paneIgnore   = "ignore"
)

var (
	reopenSessionsModes = []string{reopenAsk, reopenResume, reopenNever}
	outsidePanesModes   = []string{paneIgnore, paneRelaunch}
)

func normalizeReopenSessions(mode string) string {
	for _, known := range reopenSessionsModes {
		if mode == known {
			return mode
		}
	}
	return reopenAsk
}

func normalizeOutsidePanes(mode string) string {
	for _, known := range outsidePanesModes {
		if mode == known {
			return mode
		}
	}
	return paneIgnore
}

// reopenSessionsLabel and outsidePanesLabel are the settings rows' words.
func reopenSessionsLabel(mode string) string {
	switch normalizeReopenSessions(mode) {
	case reopenResume:
		return "always resume the ones that died"
	case reopenNever:
		return "never offer"
	}
	return "ask"
}

func outsidePanesLabel(mode string) string {
	if normalizeOutsidePanes(mode) == paneRelaunch {
		return "always relaunch into the board"
	}
	return "ignore them"
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

// paneDecisions is the per-pane ledger. A pane kept as-is has a row, so its
// answer is keyed by the row's id; a pane left out has none, so its answer is
// keyed by where it is and the process it runs (paneDecisionKey). The process
// is in the key because tmux numbers panes afresh when its server restarts,
// and a pane id alone would carry a "leave this out" onto a stranger.
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

// outsidePaneCandidates returns adopted agent panes still on the board.
func (m *Model) outsidePaneCandidates() []store.Session {
	return m.adoptedCandidates()
}

// applyPaneChoices applies the outside-pane policy to adopted rows. Relaunch
// moves each pane as it goes idle; ignore removes its row without ending it.
func (m *Model) applyPaneChoices(panes []store.Session, choice func(store.Session) string) (adopted, relaunched, ignored int) {
	if len(panes) == 0 {
		return 0, 0, 0
	}
	decided := loadPaneDecisions(m.store)
	live := map[string]bool{}
	for _, sess := range m.sessions {
		live[sess.ID] = true
	}
	// Answers for rows the board no longer has are dropped on the way past.
	for key := range decided {
		if !strings.Contains(key, "|") && !live[key] {
			delete(decided, key)
		}
	}
	var forget []store.Session
	for _, sess := range panes {
		switch choice(sess) {
		case paneRelaunch:
			if m.takeover.pending == nil {
				m.takeover.pending = map[string]bool{}
			}
			m.takeover.pending[sess.ID] = true
			relaunched++
		case paneIgnore:
			pid := 0
			if m.tmux != nil {
				pid, _ = m.tmux.PanePID(sess.ID)
			}
			decided[paneDecisionKey(sess.TmuxSocket, sess.TmuxPaneID, pid)] = paneIgnore
			delete(decided, sess.ID)
			forget = append(forget, sess)
			ignored++
		default:
			decided[sess.ID] = paneAdopt
			adopted++
		}
	}
	if err := savePaneDecisions(m.store, decided); err != nil {
		m.errBar.text = "saving the pane answers: " + err.Error()
	}
	m.forgetOutsidePanes(forget)
	if relaunched > 0 {
		m.reportTakeover(m.takeoverPass())
	}
	return adopted, relaunched, ignored
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

// paneOutcome is the one line that says what the answers did.
func paneOutcome(adopted, relaunched, ignored int) string {
	var parts []string
	if adopted > 0 {
		parts = append(parts, fmt.Sprintf("%d outside %s kept as-is", adopted, plural(adopted, "pane", "panes")))
	}
	if relaunched > 0 {
		parts = append(parts, fmt.Sprintf("%d relaunching into the board as %s idle", relaunched, plural(relaunched, "it goes", "they go")))
	}
	if ignored > 0 {
		parts = append(parts, fmt.Sprintf("%d left off the board", ignored))
	}
	return strings.Join(parts, "; ")
}
