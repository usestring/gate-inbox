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
}

// restoreCandidates matches reviveMany's own filter, so the count the prompt
// shows is exactly what accepting it revives.
func (m *Model) restoreCandidates() []store.Session {
	out := make([]store.Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		if sess.Archived || sess.Status != status.Dead || m.restoreSettled(sess) {
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

// maybeOpenRestorePrompt raises the prompt on the first refresh that could
// see real statuses. Before the first poll every row reads dead, so asking at
// Init would offer to restore a fleet that is already running.
func (m *Model) maybeOpenRestorePrompt() {
	// Armed by Init alone. A session that dies while the manager is up is
	// ordinary attrition the V key already covers; taking the screen for it
	// would interrupt whatever the operator was doing.
	if !m.restoreArmed || m.restoreAsked || m.mode != modeList {
		return
	}
	m.restoreAsked = true
	m.loadRestoreDecided()
	candidates := m.restoreCandidates()
	if len(candidates) == 0 {
		return
	}
	m.restore = restorePromptState{candidates: candidates}
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

// openRestorePicker moves to the per-session list with everything ticked, so
// the operator subtracts the few they do not want rather than reselecting a
// fleet one row at a time.
func (m *Model) openRestorePicker() {
	m.restore.picking = true
	m.restore.chosen = make(map[string]bool, len(m.restore.candidates))
	for _, sess := range m.restore.candidates {
		m.restore.chosen[sess.ID] = true
	}
}

// closeRestorePrompt is the one way out of the prompt, so it is where the
// answer is recorded: dismissing is as much a decision as restoring.
func (m *Model) closeRestorePrompt() {
	m.rememberRestoreDecisions()
	m.restore = restorePromptState{}
	m.mode = modeList
}

// commitRestore hands the ticked rows to the same bulk revive the V key uses,
// so one broken session names itself and the rest still come back.
func (m *Model) commitRestore() (tea.Model, tea.Cmd) {
	chosen := m.restoreChosen()
	m.closeRestorePrompt()
	if len(chosen) == 0 {
		// Enter on an empty picker otherwise reads as a dropped keystroke.
		m.errBar.text = "nothing selected; no panes restored"
		return m, nil
	}
	return m.reviveMany(chosen, "no dead sessions to restore")
}

func (m *Model) handleRestorePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	action, bound := m.action(keymap.ContextRestore, msg)
	if !bound {
		return m, nil
	}
	switch action {
	case keymap.Cancel:
		// From the picker, esc steps back to the count rather than out of the
		// prompt: losing a set of ticks to one keystroke would be the wrong
		// thing to make easy.
		if m.restore.picking {
			m.restore.picking = false
			m.restore.chosen = nil
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
		}
		return m, nil
	case keymap.CursorDown:
		if m.restore.picking {
			m.restore.cursor = min(m.restore.cursor+1, len(m.restore.candidates)-1)
			m.followRestoreCursor()
		}
		return m, nil
	case keymap.Toggle:
		if m.restore.picking {
			if sess, ok := m.restoreRowUnderCursor(); ok {
				m.restore.chosen[sess.ID] = !m.restore.chosen[sess.ID]
			}
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

// followRestoreCursor keeps the cursor on screen in a fleet longer than the
// card, which is the case this prompt exists for.
func (m *Model) followRestoreCursor() {
	room := m.restoreBodyRoom()
	if m.restore.cursor < m.restore.scroll {
		m.restore.scroll = m.restore.cursor
	}
	if m.restore.cursor >= m.restore.scroll+room {
		m.restore.scroll = m.restore.cursor - room + 1
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
		return [][2]string{{"↑↓", "move"}, {"space", "toggle"}, {"a", "all/none"}, {"↵", "restore"}, {"esc", "back"}}
	}
	return [][2]string{{"↵/y", "restore all"}, {"c", "choose"}, {"esc", "dismiss"}}
}

func (m *Model) restoreTitle() string {
	if m.restore.picking {
		return fmt.Sprintf("◆ Restore panes — %d of %d", m.restoreChosenCount(), len(m.restore.candidates))
	}
	return "◆ Restore panes"
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

// restoreBody lists the sessions on the summary screen and states the
// degraded split on both: the names say what is being offered, and the split
// is the reason to open the picker at all.
func (m *Model) restoreBody(inner int) []string {
	total := len(m.restore.candidates)
	exact := 0
	for _, sess := range m.restore.candidates {
		if m.resumesExactly(sess) {
			exact++
		}
	}
	if !m.restore.picking {
		lines := []string{
			fmt.Sprintf("%d %s no pane.", total, plural(total, "session has", "sessions have")),
			subtleStyle.Render("The tmux server they ran in is gone; their conversations are not."),
			"",
		}
		lines = append(lines, m.restoreSummaryList(inner)...)
		lines = append(lines, "", sectionHead("resume exactly", exact, colorAccent))
		if degraded := total - exact; degraded > 0 {
			lines = append(lines,
				sectionHead("no conversation id", degraded, colorWaiting),
				"  "+subtleStyle.Render("these resume the directory's most recent conversation instead"))
		}
		return lines
	}
	lines := make([]string, 0, total)
	for i, sess := range m.restore.candidates {
		lines = append(lines, m.restoreRow(i, sess, inner))
	}
	return lines
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
		lines = append(lines, restoreRowLine("  ", valueStyle, sess, m.resumesExactly(sess), inner))
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
	return restoreRowLine(cursor+mark+" ", nameStyle, sess, m.resumesExactly(sess), inner)
}

// restoreRowLine is the shared name-and-directory rendering both the summary
// listing and the picker use, so a session reads the same way on either.
func restoreRowLine(prefix string, nameStyle fastStyle, sess store.Session, resumesExactly bool, inner int) string {
	name := padRight(nameStyle.Render(textfmt.TruncateWidth(sess.Name, restoreNameColumn-1, "…")), restoreNameColumn)
	note := sess.Cwd
	if !resumesExactly {
		note = "no conversation id · " + note
	}
	if room := inner - restoreNameColumn - 6; room > 8 {
		note = textfmt.TruncateWidth(note, room, "…")
	}
	return prefix + name + subtleStyle.Render(note)
}
