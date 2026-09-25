package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A manager that starts up to find its panes gone -- the tmux server was
// restarted, or the box rebooted -- already holds everything needed to bring
// the fleet back, and until now said nothing about it: the operator had to
// notice the dead rows and know that V revives them. This asks once, on the
// first pass whose statuses are real, and never again in the same run.
//
// It offers both the whole set and a picker, because restoring is not free.
// Every revived session is an agent process, and a fleet large enough to
// have filled a box once will fill it again if it all comes back at once.
//
// The answer is kept. A prompt that came back every start would be one the
// operator dismisses without reading, so a row that has been answered for is
// settled until it dies again -- see the ledger in store.RestoreDecided.

type restorePromptState struct {
	// candidates are the dead rows the prompt offers, in the order the list
	// shows them, so the picker reads the same way round as the board.
	candidates []store.Session
	// chosen carries the picker's ticks by session id. It stays nil until
	// the picker opens, where every row starts ticked.
	chosen map[string]bool
	cursor int
	scroll int
	// picking is the second screen, the per-session list. The first is the
	// count and the three answers.
	picking bool
	// ends is why each dead row the offer considered is dead, including the
	// ones it left out for having been ended on purpose.
	ends endLedger
	// panes are the agent panes started outside the board that the card
	// asks about (reopenpanes.go). paneDefault is the summary's answer for
	// all of them; paneChoice is the picker's answer per pane, by id.
	panes       []store.Session
	paneDefault string
	paneChoice  map[string]string
	// notices are what the stored defaults did quietly on the way in, said
	// once the card closes.
	notices []string
}

// restoreCandidates matches reviveMany's own filter, so the count the prompt
// shows is exactly what accepting it revives.
//
// A row the operator ended on purpose is never a candidate; see endclass.go.
// The verdicts are kept on the prompt so the card can say why each row is
// there, and how many were left out.
func (m *Model) restoreCandidates() []store.Session {
	return m.classifyRestore(m.restoreEvidence)
}

func (m *Model) classifyRestore(ev endEvidence) []store.Session {
	out := make([]store.Session, 0, len(m.sessions))
	m.restore.ends = endLedger{}
	for _, sess := range m.sessions {
		if sess.Archived || sess.Status != status.Dead {
			continue
		}
		class := classifyEnd(sess, ev)
		if live, running := m.supersededBy(sess); running {
			class = endClass{endSuperseded, "its conversation is running in " + live.Name}
		}
		m.restore.ends[sess.ID] = class
		if class.verdict == endByOperator || class.verdict == endSuperseded || m.restoreSettled(sess) {
			continue
		}
		out = append(out, sess)
	}
	return out
}

// restoreSettled reports whether the operator has already answered for this
// row. The mark has to name the same agent run: a session answered for,
// brought back by hand, and lost again is a loss nobody has seen an offer
// about, and reviving it moves LaunchTime.
func (m *Model) restoreSettled(sess store.Session) bool {
	at, marked := m.restoreDecided[sess.ID]
	return marked && at.Equal(sess.LaunchTime())
}

// loadRestoreDecided reads the ledger on the one pass that needs it. A store
// that cannot be read leaves it empty and the offer stands: asking twice is a
// nuisance, never asking leaves a fleet to be revived a row at a time.
func (m *Model) loadRestoreDecided() {
	if m.store == nil {
		return
	}
	decided, err := m.store.RestoreDecided()
	if err != nil {
		logging.Info("restore decisions unreadable", logging.Err(err))
		return
	}
	m.restoreDecided = decided
}

// rememberRestoreDecisions settles every row the prompt offered, whichever
// answer it got and whether or not the revive that followed worked: the offer
// was about the loss, and the loss has now been put to the operator. Marks
// for rows the board no longer carries are dropped on the way past, which is
// the only pruning the ledger needs.
func (m *Model) rememberRestoreDecisions() {
	if len(m.restore.candidates) == 0 {
		return
	}
	decided := make(map[string]time.Time, len(m.restoreDecided)+len(m.restore.candidates))
	for _, sess := range m.sessions {
		if at, marked := m.restoreDecided[sess.ID]; marked {
			decided[sess.ID] = at
		}
	}
	for _, sess := range m.restore.candidates {
		decided[sess.ID] = sess.LaunchTime()
	}
	m.restoreDecided = decided
	if m.store == nil {
		return
	}
	if err := m.store.SetRestoreDecided(decided); err != nil {
		m.errBar.text = err.Error()
	}
}

// resumesExactly reports whether a candidate can come back on its own
// conversation rather than the working directory's most recent one, which is
// the wrong conversation whenever sessions share a directory. It is the one
// distinction the prompt draws, because it is the one the operator cannot
// undo after the fact.
func (m *Model) resumesExactly(sess store.Session) bool {
	tool, known := m.cfg.Tools[sess.Tool]
	return known && sess.AgentSessionID != "" && tool.ResumeByIDCommand != ""
}

// reopenSettleLimit is how many passes the reopen card waits for the rows the
// first adopt scan created to show on the board before asking without them.
const reopenSettleLimit = 5

// adoptScanSettled reports whether the panes found at start are on the board,
// which is when the card can ask about them. A board that cannot scan has
// nothing to wait for.
func (m *Model) adoptScanSettled() bool {
	if m.store == nil || m.tmux == nil {
		return true
	}
	if m.adoptFirstDone {
		shown := make(map[string]bool, len(m.sessions))
		for _, sess := range m.sessions {
			shown[sess.ID] = true
		}
		missing := false
		for _, id := range m.adoptFirstIDs {
			if !shown[id] {
				missing = true
			}
		}
		if !missing {
			return true
		}
	}
	m.adoptSettleWaits++
	return m.adoptSettleWaits > reopenSettleLimit
}

// noteAdopted records what an adopt scan took. The first scan's rows are what
// the reopen card waits for; a later scan's are panes started while the board
// was up, which get the stored answer quietly or a one-line pointer to O.
func (m *Model) noteAdopted(msg adoptedMsg) {
	if !m.adoptFirstDone {
		m.adoptFirstDone = true
		m.adoptFirstIDs = msg.ids
		return
	}
	if len(msg.ids) == 0 {
		return
	}
	n := len(msg.ids)
	switch m.outsidePanesMode() {
	case reopenAsk:
		m.reportDone(fmt.Sprintf("%d agent %s started outside the board added as-is; O to relaunch or leave %s out",
			n, plural(n, "pane", "panes"), plural(n, "it", "them")))
	case paneRelaunch:
		if m.takeover.pending == nil {
			m.takeover.pending = map[string]bool{}
		}
		for _, id := range msg.ids {
			m.takeover.pending[id] = true
		}
		m.reportDone(fmt.Sprintf("%d outside %s will relaunch into the board once idle (settings: outside panes)",
			n, plural(n, "pane", "panes")))
	}
}

// maybeOpenRestorePrompt raises the reopen card on the first refresh that
// could see real statuses, once the panes found at start are on the board.
// Before the first poll every row reads dead, so asking at Init would offer to
// restore a fleet that is already running.
//
// Each half first consults its setting. "ask" puts it on the card; the others
// apply the stored answer quietly and leave one line saying so.
func (m *Model) maybeOpenRestorePrompt() {
	// Armed by Init alone. A session that dies while the manager is up is
	// ordinary attrition the V key already covers; taking the screen for it
	// would interrupt whatever the operator was doing.
	if !m.restoreArmed || m.restoreAsked || m.mode != modeList {
		return
	}
	if !m.adoptScanSettled() {
		return
	}
	m.restoreAsked = true
	m.loadRestoreDecided()
	m.restoreEvidence = m.loadEndEvidence()
	candidates := m.restoreCandidates()
	ends := m.restore.ends
	var notices []string

	switch m.reopenSessionsMode() {
	case reopenResume:
		var died []store.Session
		for _, sess := range candidates {
			if ends[sess.ID].verdict == endDied {
				died = append(died, sess)
			}
		}
		if len(died) > 0 {
			m.reviveMany(died, "")
			notices = append(notices, fmt.Sprintf("resumed %d %s that died (settings: on reopen)",
				len(died), plural(len(died), "session", "sessions")))
		}
		if unclear := len(candidates) - len(died); unclear > 0 {
			notices = append(notices, fmt.Sprintf("%d with an unclear end left for V", unclear))
		}
		candidates = nil
	case reopenNever:
		if len(candidates) > 0 {
			notices = append(notices, fmt.Sprintf("%d %s stopped while the board was closed; V revives (settings: on reopen)",
				len(candidates), plural(len(candidates), "session", "sessions")))
		}
		candidates = nil
	}

	panes := m.outsidePaneCandidates(false)
	if mode := m.outsidePanesMode(); mode != reopenAsk && len(panes) > 0 {
		outcome := paneOutcome(m.applyPaneChoices(panes, func(store.Session) string { return mode }))
		notices = append(notices, outcome+" (settings: outside panes)")
		panes = nil
	}

	if len(candidates) == 0 && len(panes) == 0 {
		if len(notices) > 0 {
			m.reportDone(strings.Join(notices, "; "))
		}
		return
	}
	m.restore = restorePromptState{candidates: candidates, ends: ends, panes: panes, paneDefault: paneAdopt, notices: notices}
	m.mode = modeRestorePrompt
}

// restoreChosen is what accepting the picker revives. The summary screen has
// no ticks of its own, so it stands for every candidate.
func (m *Model) restoreChosen() []store.Session {
	if !m.restore.picking {
		return m.restore.candidates
	}
	out := make([]store.Session, 0, len(m.restore.candidates))
	for _, sess := range m.restore.candidates {
		if m.restore.chosen[sess.ID] {
			out = append(out, sess)
		}
	}
	return out
}

// restoreChosenCount is the tick count without the slice restoreChosen would
// build, which the title asks for on every frame.
func (m *Model) restoreChosenCount() int {
	if !m.restore.picking {
		return len(m.restore.candidates)
	}
	n := 0
	for _, sess := range m.restore.candidates {
		if m.restore.chosen[sess.ID] {
			n++
		}
	}
	return n
}

// openRestorePicker moves to the per-item list with every session ticked and
// every pane on the summary's answer, so the operator changes the few they
// want different rather than answering a fleet one row at a time.
func (m *Model) openRestorePicker() {
	m.restore.picking = true
	m.restore.chosen = make(map[string]bool, len(m.restore.candidates))
	for _, sess := range m.restore.candidates {
		m.restore.chosen[sess.ID] = true
	}
	m.restore.paneChoice = make(map[string]string, len(m.restore.panes))
	for _, sess := range m.restore.panes {
		m.restore.paneChoice[sess.ID] = m.restore.paneDefault
	}
}

// restorePaneChoice is the answer for one pane: the picker's when it is open,
// the summary's otherwise.
func (m *Model) restorePaneChoice(sess store.Session) string {
	if m.restore.picking {
		if choice, ok := m.restore.paneChoice[sess.ID]; ok {
			return choice
		}
	}
	if m.restore.paneDefault == "" {
		return paneAdopt
	}
	return m.restore.paneDefault
}

// closeRestorePrompt is the one way out of the card, so it is where the
// answer is recorded: dismissing is as much a decision as restoring. Panes
// dismissed are kept as they are, and remembered as answered.
func (m *Model) closeRestorePrompt() {
	m.reportRestoreNotices(m.finishRestorePrompt(func(store.Session) string { return paneAdopt }))
}

// reportRestoreNotices says what the card and the stored defaults did, after
// whatever went wrong on the way when something did.
func (m *Model) reportRestoreNotices(notices []string) {
	if len(notices) == 0 {
		return
	}
	if m.errBar.text != "" && !m.errBar.worked() {
		// A warning stays a warning, with the card's outcome after it: the
		// "won't ask again" line must not be lost to a degraded revive.
		m.errBar.text += "; " + strings.Join(notices, "; ")
		return
	}
	if m.errBar.text != "" {
		notices = append([]string{m.errBar.text}, notices...)
	}
	m.reportDone(strings.Join(notices, "; "))
}

// finishRestorePrompt records the session answer, applies the pane answers
// and returns to the list, handing back what the card has to report.
func (m *Model) finishRestorePrompt(choice func(store.Session) string) []string {
	m.rememberRestoreDecisions()
	panes, notices := m.restore.panes, m.restore.notices
	m.restore = restorePromptState{}
	m.restoreEvidence = endEvidence{}
	m.mode = modeList
	if outcome := paneOutcome(m.applyPaneChoices(panes, choice)); outcome != "" {
		notices = append(notices, outcome)
	}
	return notices
}

// commitRestore hands the ticked sessions to the same bulk revive the V key
// uses, so one broken session names itself and the rest still come back, and
// carries out each pane's answer.
func (m *Model) commitRestore() (tea.Model, tea.Cmd) {
	chosen := m.restoreChosen()
	offered := len(m.restore.candidates) > 0
	answers := make(map[string]string, len(m.restore.panes))
	for _, sess := range m.restore.panes {
		answers[sess.ID] = m.restorePaneChoice(sess)
	}
	notices := m.finishRestorePrompt(func(sess store.Session) string { return answers[sess.ID] })
	if !offered {
		m.reportRestoreNotices(notices)
		return m, nil
	}
	if len(chosen) == 0 {
		// Enter on an empty picker otherwise reads as a dropped keystroke.
		m.errBar.text = "nothing selected; no sessions resumed"
		return m, nil
	}
	model, cmd := m.reviveMany(chosen, "no dead sessions to restore")
	m.reportRestoreNotices(notices)
	return model, cmd
}

// neverAskAgain applies the card's answer and stores it as the default, so
// the next start does the same without asking. The settings screen is where
// it is turned back to asking, and the notice says so.
func (m *Model) neverAskAgain() (tea.Model, tea.Cmd) {
	var saved []string
	if len(m.restore.candidates) > 0 {
		mode := reopenNever
		if m.restoreChosenCount() > 0 {
			mode = reopenResume
		}
		if m.store != nil {
			if err := m.store.SetSetting(reopenSessionsSetting, mode); err != nil {
				m.errBar.text = err.Error()
			}
		}
		saved = append(saved, "on reopen: "+reopenSessionsLabel(mode))
	}
	if len(m.restore.panes) > 0 {
		mode := m.restore.paneDefault
		if m.store != nil {
			if err := m.store.SetSetting(outsidePanesSetting, mode); err != nil {
				m.errBar.text = err.Error()
			}
		}
		saved = append(saved, "outside panes: "+outsidePanesLabel(mode))
	}
	m.restore.notices = append(m.restore.notices,
		"won't ask again ("+strings.Join(saved, ", ")+"); change it in settings (s)")
	return m.commitRestore()
}

func (m *Model) handleRestorePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	action, bound := m.action(keymap.ContextRestore, msg)
	if !bound {
		return m, nil
	}
	rows := len(m.restore.candidates) + len(m.restore.panes)
	switch action {
	case keymap.Cancel:
		// From the picker, esc steps back to the summary rather than out of
		// the card: losing a set of ticks to one keystroke would be the
		// wrong thing to make easy.
		if m.restore.picking {
			m.restore.picking = false
			m.restore.chosen, m.restore.paneChoice = nil, nil
			m.restore.cursor, m.restore.scroll = 0, 0
			return m, nil
		}
		m.closeRestorePrompt()
		return m, nil
	case keymap.More:
		if !m.restore.picking {
			m.openRestorePicker()
		}
		return m, nil
	case keymap.CursorUp:
		if m.restore.picking {
			m.restore.cursor = max(m.restore.cursor-1, 0)
			m.followRestoreCursor()
		} else {
			m.restore.scroll = max(m.restore.scroll-1, 0)
		}
		return m, nil
	case keymap.CursorDown:
		if m.restore.picking {
			m.restore.cursor = min(m.restore.cursor+1, rows-1)
			m.followRestoreCursor()
		} else {
			// The summary is read, not picked from: the arrows scroll it,
			// since what a kept pane misses runs past a short terminal.
			limit := len(m.restoreBody(cardInnerWidth(helpCardWidth(m.width)))) - m.restoreBodyRoom()
			m.restore.scroll = min(m.restore.scroll+1, max(limit, 0))
		}
		return m, nil
	case keymap.Toggle, keymap.NextChoice, keymap.PrevChoice:
		step := 1
		if action == keymap.PrevChoice {
			step = -1
		}
		if !m.restore.picking {
			if action != keymap.Toggle && len(m.restore.panes) > 0 {
				m.restore.paneDefault = nextPaneChoice(m.restore.paneDefault, step)
			}
			return m, nil
		}
		if sess, ok := m.restoreRowUnderCursor(); ok {
			m.restore.chosen[sess.ID] = !m.restore.chosen[sess.ID]
		} else if pane, ok := m.restorePaneUnderCursor(); ok {
			m.restore.paneChoice[pane.ID] = nextPaneChoice(m.restorePaneChoice(pane), step)
		}
		return m, nil
	case keymap.TickAll:
		if m.restore.picking {
			// One key for both directions: with anything unticked "a" means
			// take all, and from a full set it means start over.
			all := m.restoreChosenCount() == len(m.restore.candidates)
			for _, sess := range m.restore.candidates {
				m.restore.chosen[sess.ID] = !all
			}
		}
		return m, nil
	case keymap.NeverAsk:
		return m.neverAskAgain()
	case keymap.Confirm:
		return m.commitRestore()
	}
	return m, nil
}

func (m *Model) restoreRowUnderCursor() (store.Session, bool) {
	if m.restore.cursor < 0 || m.restore.cursor >= len(m.restore.candidates) {
		return store.Session{}, false
	}
	return m.restore.candidates[m.restore.cursor], true
}

// restorePaneUnderCursor is the pane row under the cursor; the picker lists
// the panes after the sessions.
func (m *Model) restorePaneUnderCursor() (store.Session, bool) {
	i := m.restore.cursor - len(m.restore.candidates)
	if i < 0 || i >= len(m.restore.panes) {
		return store.Session{}, false
	}
	return m.restore.panes[i], true
}

// followRestoreCursor keeps the cursor on screen in a fleet longer than the
// card, which is the case this prompt exists for. The picker draws a heading
// above the panes, so a pane row sits one line further down than its index.
func (m *Model) followRestoreCursor() {
	room := m.restoreBodyRoom()
	line := m.restore.cursor
	if len(m.restore.candidates) > 0 && m.restore.cursor >= len(m.restore.candidates) {
		line += 2
	}
	if line < m.restore.scroll {
		m.restore.scroll = line
	}
	if line >= m.restore.scroll+room {
		m.restore.scroll = line - room + 1
	}
	m.restore.scroll = max(m.restore.scroll, 0)
}

func (m *Model) restoreBodyRoom() int {
	inner := cardInnerWidth(helpCardWidth(m.width))
	room := m.height - 5 - lipgloss.Height(legendInline(m.restoreHint(), inner))
	if m.errBar.text != "" {
		room -= 2
	}
	return max(room, 1)
}

func (m *Model) restoreHint() [][2]string {
	if m.restore.picking {
		hint := [][2]string{{"↑↓", "move"}}
		if len(m.restore.candidates) > 0 {
			hint = append(hint, [2]string{"space", "tick"}, [2]string{"a", "all/none"})
		}
		if len(m.restore.panes) > 0 {
			hint = append(hint, [2]string{"←→", "pane answer"})
		}
		return append(hint, [2]string{"↵", "apply"}, [2]string{"N", "never ask"}, [2]string{"esc", "back"})
	}
	hint := [][2]string{{"↵/y", "apply"}, {"↑↓", "scroll"}}
	if len(m.restore.panes) > 0 {
		hint = append(hint, [2]string{"←→", "pane answer"})
	}
	return append(hint, [2]string{"c", "choose"}, [2]string{"N", "never ask"}, [2]string{"esc", "leave as is"})
}

func (m *Model) restoreTitle() string {
	switch {
	case len(m.restore.candidates) == 0:
		return "◆ Panes started outside the board"
	case m.restore.picking && len(m.restore.panes) == 0:
		return fmt.Sprintf("◆ Welcome back — %d of %d", m.restoreChosenCount(), len(m.restore.candidates))
	}
	return "◆ Welcome back"
}

func (m *Model) viewRestorePrompt() string {
	width := helpCardWidth(m.width)
	inner := cardInnerWidth(width)
	body := fitBody(m.restoreBody(inner), m.restoreBodyRoom(), m.restore.scroll)
	return m.cardSized(width, m.restoreTitle(), strings.Join(body, "\n"), m.restoreHint())
}

// restoreListLimit caps how many sessions the summary screen names, so a
// fleet large enough to need the picker still fits the card. The picker is
// where the whole set is visible.
const restoreListLimit = 10

// restoreBody is the card: the board's own sessions that stopped, then the
// panes started outside it, then how to stop being asked.
func (m *Model) restoreBody(inner int) []string {
	if m.restore.picking {
		return m.restorePickerBody(inner)
	}
	var lines []string
	if len(m.restore.candidates) > 0 {
		lines = append(lines, m.sessionsBody(inner)...)
	}
	if len(m.restore.panes) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "", sectionStyle.Render("outside the board"), "")
		}
		lines = append(lines, m.panesBody(inner)...)
	}
	lines = append(lines, "")
	for _, line := range textfmt.Wrap("N applies this answer and stops asking; settings (s) has \"on reopen\" and \"outside panes\" to ask again.", inner) {
		lines = append(lines, subtleStyle.Render(line))
	}
	return lines
}

// restorePickerBody lists every session, ticked or not, then every pane with
// its answer.
func (m *Model) restorePickerBody(inner int) []string {
	lines := make([]string, 0, len(m.restore.candidates)+len(m.restore.panes)+2)
	for i, sess := range m.restore.candidates {
		lines = append(lines, m.restoreRow(i, sess, inner))
	}
	if len(m.restore.panes) == 0 {
		return lines
	}
	if len(m.restore.candidates) > 0 {
		lines = append(lines, "", sectionStyle.Render("outside the board"))
	}
	for i, sess := range m.restore.panes {
		cursor := "  "
		if len(m.restore.candidates)+i == m.restore.cursor {
			cursor = "▸ "
		}
		lines = append(lines, paneRowLine(cursor+"  ", valueStyle, sess, m.restorePaneChoice(sess), inner))
	}
	return lines
}

// sessionsBody is the summary of the board's own sessions that stopped
// without the operator ending them, and the split that is the reason to open
// the picker at all.
func (m *Model) sessionsBody(inner int) []string {
	total := len(m.restore.candidates)
	exact := 0
	for _, sess := range m.restore.candidates {
		if m.resumesExactly(sess) {
			exact++
		}
	}
	lines := []string{
		fmt.Sprintf("%d %s without you ending %s.", total, plural(total, "session stopped", "sessions stopped"), plural(total, "it", "them")),
		subtleStyle.Render("Their panes are gone; their conversations are not. ↵ resumes them."),
		"",
	}
	lines = append(lines, m.restoreSummaryList(inner)...)
	lines = append(lines, "", sectionHead("died", m.restoreVerdictCount(endDied), colorAccent))
	if unknown := m.restoreVerdictCount(endUnknown); unknown > 0 {
		lines = append(lines,
			sectionHead("unclear", unknown, colorWaiting),
			"  "+subtleStyle.Render("no record says how these ended; check before bringing them back"))
	}
	lines = append(lines, sectionHead("resume exactly", exact, colorAccent))
	if degraded := total - exact; degraded > 0 {
		lines = append(lines,
			sectionHead("no conversation id", degraded, colorWaiting),
			"  "+subtleStyle.Render("these resume the directory's most recent conversation instead"))
	}
	if ended := m.restore.ends.count(endByOperator); ended > 0 {
		lines = append(lines, "")
		for _, line := range textfmt.Wrap(fmt.Sprintf(
			"%d more you ended yourself %s not offered: killed, archived, parked, quit with /exit, or closed in tmux.",
			ended, plural(ended, "is", "are")), inner) {
			lines = append(lines, subtleStyle.Render(line))
		}
	}
	if running := m.restore.ends.count(endSuperseded); running > 0 {
		for _, line := range textfmt.Wrap(fmt.Sprintf(
			"%d more %s not offered: %s conversation is already running in another pane on the board.",
			running, plural(running, "is", "are"), plural(running, "its", "each one's")), inner) {
			lines = append(lines, subtleStyle.Render(line))
		}
	}
	return lines
}

// restoreVerdictCount counts the offered rows with a verdict. The ledger also
// holds the rows left out, so it is filtered by what is on the card.
func (m *Model) restoreVerdictCount(verdict endVerdict) int {
	n := 0
	for _, sess := range m.restore.candidates {
		if m.restore.ends[sess.ID].verdict == verdict {
			n++
		}
	}
	return n
}

// restoreWhy is the reason a row is on the card, with how long ago it was
// last seen when the reason alone cannot settle it.
func (m *Model) restoreWhy(sess store.Session) string {
	class, ok := m.restore.ends[sess.ID]
	if !ok {
		return ""
	}
	if class.verdict == endUnknown && !sess.LastStatusAt.IsZero() {
		return class.why + ", last seen " + shortAge(time.Since(sess.LastStatusAt)) + " ago"
	}
	return class.why
}

// shortAge is a duration the way a person says it about something they last
// saw: the largest unit, rounded down.
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// restoreSummaryList names the candidates so the operator can see which
// sessions are missing a pane rather than only how many. A fleet longer than
// restoreListLimit is trimmed, and the line left in its place says how many
// were held back so the count still adds up.
func (m *Model) restoreSummaryList(inner int) []string {
	shown := m.restore.candidates
	if len(shown) > restoreListLimit {
		shown = shown[:restoreListLimit]
	}
	lines := make([]string, 0, len(shown)+1)
	for _, sess := range shown {
		lines = append(lines, restoreRowLine("  ", valueStyle, sess, m.resumesExactly(sess), m.restoreWhy(sess), inner))
	}
	if hidden := len(m.restore.candidates) - len(shown); hidden > 0 {
		lines = append(lines, "  "+subtleStyle.Render(
			fmt.Sprintf("… and %d more; press c to choose", hidden)))
	}
	return lines
}

// restoreNameColumn is how much of a name a row shows before the directory,
// which is what tells two same-named sessions apart.
const restoreNameColumn = 24

func (m *Model) restoreRow(i int, sess store.Session, inner int) string {
	mark := " "
	if m.restore.chosen[sess.ID] {
		mark = "✓"
	}
	cursor := "  "
	if i == m.restore.cursor {
		cursor = "▸ "
	}
	nameStyle := valueStyle
	if !m.restore.chosen[sess.ID] {
		nameStyle = subtleStyle
	}
	return restoreRowLine(cursor+mark+" ", nameStyle, sess, m.resumesExactly(sess), m.restoreWhy(sess), inner)
}

// restoreRowLine is the shared name-and-directory rendering both the summary
// listing and the picker use, so a session reads the same way on either.
// why leads the note, since it is what the operator decides on.
func restoreRowLine(prefix string, nameStyle fastStyle, sess store.Session, resumesExactly bool, why string, inner int) string {
	name := padRight(nameStyle.Render(textfmt.TruncateWidth(sess.Name, restoreNameColumn-1, "…")), restoreNameColumn)
	note := sess.Cwd
	if !resumesExactly {
		note = "no conversation id · " + note
	}
	if why != "" {
		note = why + " · " + note
	}
	if room := inner - restoreNameColumn - 6; room > 8 {
		note = textfmt.TruncateWidth(note, room, "…")
	}
	return prefix + name + subtleStyle.Render(note)
}
