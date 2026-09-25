package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Reopening the board after a while away has two questions in it, and they
// used to be asked one after the other, by two different prompts: which of
// the board's own sessions to bring back, and what to do about agent panes
// somebody started by hand in the meantime. This file is the second half of
// the one card that now asks both (restoreprompt.go is the first), plus the
// settings that let an operator answer either once and for all.
//
// The adopt scan has always taken those panes on its own, so a pane is on
// the board as-is before the card is up. The card's job is to say what that
// means -- what an adopted pane cannot do that a board session can -- and to
// offer the two other answers: relaunch it into the board, or leave it out.

// Settings keys and their values. "ask" is the default for both.
const (
	reopenSessionsSetting = "reopen_sessions"
	outsidePanesSetting   = "outside_panes"
	// outsidePanesDecidedSetting remembers the per-pane answers, so a pane
	// kept as-is or left out is not asked about again. See paneDecisions.
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
	outsidePanesModes   = []string{reopenAsk, paneAdopt, paneRelaunch, paneIgnore}
	// paneChoices are the answers the card cycles through for a pane.
	paneChoices = []string{paneAdopt, paneRelaunch, paneIgnore}
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
	return reopenAsk
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
	switch normalizeOutsidePanes(mode) {
	case paneAdopt:
		return "always adopt as-is"
	case paneRelaunch:
		return "always relaunch into the board"
	case paneIgnore:
		return "ignore them"
	}
	return "ask"
}

// paneChoiceLabel is how the card names one answer.
func paneChoiceLabel(choice string) string {
	switch choice {
	case paneRelaunch:
		return "relaunch into the board"
	case paneIgnore:
		return "ignore"
	}
	return "adopt as-is"
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

// outsidePaneMisses is what a pane kept as-is cannot do that a board session
// can, derived from the refusals and launch-time wiring in the code, in the
// order an operator is most likely to notice them.
var outsidePaneMisses = []string{
	"the board's MCP tools inside the agent: spawning, messaging, tasks",
	"reach from other sessions and the CLI: send, read, answer, wait, kill",
	"guaranteed hook status (Claude Code): questions and permission prompts are read off the screen",
	"the board's launch environment: GATE_INBOX_* variables, extension settings, a fresh account token",
	"a known conversation unless Claude Code names it: fork, move to another CLI, resume, restart, revive",
	"the back-to-board keys and the pane's title and colours",
}

// outsidePaneRelaunchCost is what relaunching costs, said once.
const outsidePaneRelaunchCost = "Relaunching ends each pane once it is idle and resumes the same conversation " +
	"as a board session on the board's tmux server. The flags and model it was started with are not kept."

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

// outsidePaneCandidates is every live adopted agent pane the operator has not
// answered for yet. all includes the answered ones, which is what O shows.
func (m *Model) outsidePaneCandidates(all bool) []store.Session {
	candidates := m.adoptedCandidates()
	if all {
		return candidates
	}
	decided := loadPaneDecisions(m.store)
	out := candidates[:0:0]
	for _, sess := range candidates {
		if decided[sess.ID] == "" {
			out = append(out, sess)
		}
	}
	return out
}

// applyPaneChoices carries out the card's answers, or the stored default's.
// Adopt keeps the pane as it is and remembers the answer; relaunch hands it to
// the takeover, which moves each pane as it goes idle; ignore takes it off the
// board without touching the pane and keeps the scan from taking it again.
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

// panesBody is the card's section for panes started outside the board.
func (m *Model) panesBody(inner int) []string {
	panes := m.restore.panes
	if len(panes) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("%d agent %s started outside the board.", len(panes), plural(len(panes), "pane was", "panes were"))}
	intro := "They are on the board as-is. Keep them that way, relaunch them into the board, or leave them off it."
	if len(panes) == 1 {
		intro = "It is on the board as-is. Keep it that way, relaunch it into the board, or leave it off it."
	}
	for _, line := range textfmt.Wrap(intro, inner) {
		lines = append(lines, subtleStyle.Render(line))
	}
	lines = append(lines, "")
	shown := panes
	if len(shown) > restoreListLimit {
		shown = shown[:restoreListLimit]
	}
	for _, sess := range shown {
		lines = append(lines, paneRowLine("  ", valueStyle, sess, "", inner))
	}
	if hidden := len(panes) - len(shown); hidden > 0 {
		lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("… and %d more; press c to choose", hidden)))
	}
	lines = append(lines, "", m.paneChoiceLine())
	lines = append(lines, "", subtleStyle.Render("Kept as-is, a pane keeps running where it is but misses:"))
	for _, miss := range outsidePaneMisses {
		for i, line := range textfmt.Wrap(miss, max(inner-4, 8)) {
			lead := "  · "
			if i > 0 {
				lead = "    "
			}
			lines = append(lines, lead+mutedStyle.Render(line))
		}
	}
	lines = append(lines, "")
	for _, line := range textfmt.Wrap(outsidePaneRelaunchCost, inner) {
		lines = append(lines, subtleStyle.Render(line))
	}
	return lines
}

// paneChoiceLine shows the summary's answer for every pane, the chosen one
// marked, so ←→ reads as moving between them.
func (m *Model) paneChoiceLine() string {
	parts := make([]string, 0, len(paneChoices))
	for _, choice := range paneChoices {
		label := paneChoiceLabel(choice)
		if choice == m.restore.paneDefault {
			parts = append(parts, keyStyle.Render("["+label+"]"))
		} else {
			parts = append(parts, mutedStyle.Render(label))
		}
	}
	return "  " + strings.Join(parts, "  ")
}

// paneRowLine renders one pane: its name, then its tool and where it runs.
func paneRowLine(prefix string, nameStyle fastStyle, sess store.Session, choice string, inner int) string {
	name := padRight(nameStyle.Render(textfmt.TruncateWidth(sess.Name, restoreNameColumn-1, "…")), restoreNameColumn)
	note := sess.Tool + " · " + sess.Cwd
	if sess.Status != status.Idle && sess.Status != "" {
		note = sess.Status + " · " + note
	}
	if choice != "" {
		note = paneChoiceLabel(choice) + " · " + note
	}
	if room := inner - restoreNameColumn - 6; room > 8 {
		note = textfmt.TruncateWidth(note, room, "…")
	}
	return prefix + name + subtleStyle.Render(note)
}

// nextPaneChoice steps an answer through paneChoices.
func nextPaneChoice(choice string, step int) string {
	index := 0
	for i, known := range paneChoices {
		if known == choice {
			index = i
		}
	}
	return paneChoices[(index+step+len(paneChoices))%len(paneChoices)]
}
