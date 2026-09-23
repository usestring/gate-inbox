package ui

import (
	"fmt"
	"github.com/usestring/gate-inbox/internal/keymap"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/namesweep"
	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// The name sweep asks adopted agents to name themselves. It is the one thing
// in the manager that types into panes it did not start, in bulk, so it is
// opt-in, it shows its whole target list before it sends a keystroke, and it
// re-asks both gates at the door. Nothing here runs on a timer.

type nameSweepState struct {
	plan     namesweep.Plan
	result   *namesweep.Result
	scroll   int
	building bool
	sending  bool
	// stop ends a sweep part way through. Closed by esc while sending.
	stop chan struct{}
}

type nameSweepPlanMsg struct {
	plan namesweep.Plan
}

type nameSweepDoneMsg struct {
	result namesweep.Result
}

// openNameSweep builds the dry run.
//
// Everything the plan needs is read here, on the event loop, and the reading
// of ninety transcripts happens in the returned closure. Reading the model
// from inside a command is a data race: commands run on their own goroutine
// while Update writes.
func (m *Model) openNameSweep() tea.Cmd {
	if m.store == nil || m.tmux == nil {
		return nil
	}
	m.nameSweep = nameSweepState{building: true}
	m.mode = modeNameSweep
	m.errBar.text = ""

	candidates := m.nameSweepCandidates()
	reader := promptcache.NewReader(promptcache.DefaultRoot())
	directive := m.nameSweepDirective()
	return func() tea.Msg {
		plan := namesweep.Build(candidates,
			func(c namesweep.Candidate) promptcache.State { return reader.Lookup(c.Cwd, c.AgentSessionID) },
			directive, time.Now())
		return nameSweepPlanMsg{plan: plan}
	}
}

func (m *Model) nameSweepCandidates() []namesweep.Candidate {
	candidates := make([]namesweep.Candidate, 0, len(m.sessions))
	for _, sess := range m.sessions {
		candidates = append(candidates, namesweep.Candidate{
			ID:             sess.ID,
			Name:           sess.Name,
			Tool:           sess.Tool,
			Cwd:            sess.Cwd,
			Status:         sess.Status,
			AgentSessionID: sess.AgentSessionID,
			Adopted:        sess.TmuxPaneID != "",
			CacheReadable:  m.cacheReadable(sess.Tool),
			Archived:       sess.Archived,
			Shell:          m.isShell(sess.Tool),
		})
	}
	return candidates
}

// cacheReadable marks the tools whose prompt-cache state this program can
// price. Claude Code writes the cache numbers into its own transcript;
// nothing else does, and an unpriced session is treated as a cold one.
func (m *Model) cacheReadable(tool string) bool {
	return m.cfg.Tools[tool].StatusSource == hooks.StatusSourceClaude
}

// nameSweepDirective renders the exact text one adopted pane would receive.
// Both values it closes over are read here rather than inside the returned
// function, which the plan calls from the command goroutine.
func (m *Model) nameSweepDirective() func(namesweep.Candidate) string {
	// hooks.Manager holds the hooks subdirectory, so the config directory the
	// rename subcommand needs is its parent.
	configDir := filepath.Dir(m.hooks.Dir())
	executable := launch.Executable()
	return func(candidate namesweep.Candidate) string {
		return launch.AdoptedRenameDirective(
			launch.AdoptedRenameCommand(executable, configDir, candidate.ID))
	}
}

// sendNameSweep types the directive into every approved pane.
//
// Snapshotted on the event loop for the same reason the plan is: the sweep
// runs for minutes on its own goroutine while Update keeps writing the model.
func (m *Model) sendNameSweep() tea.Cmd {
	targets := append([]namesweep.Verdict(nil), m.nameSweep.plan.Targets...)
	if len(targets) == 0 {
		return nil
	}
	stop := make(chan struct{})
	m.nameSweep.sending = true
	m.nameSweep.stop = stop
	pace := m.cfg.NameSweepPace.Duration
	gate := sweepGate{
		tmux:   m.tmux,
		engine: m.engine,
		cache:  promptcache.NewReader(promptcache.DefaultRoot()),
	}
	send := m.tmux.SendText
	return func() tea.Msg {
		return nameSweepDoneMsg{result: namesweep.Send(targets, gate, send, time.Now, pace, time.Sleep, stop)}
	}
}

// sweepGate re-answers both gates for one pane immediately before its send.
type sweepGate struct {
	tmux   *tmux.Driver
	engine *status.Engine
	cache  *promptcache.Reader
}

func (g sweepGate) Status(target namesweep.Verdict) (string, error) {
	if !g.tmux.Exists(target.ID) {
		return status.Dead, nil
	}
	pane, err := g.tmux.CapturePane(target.ID)
	if err != nil {
		return "", err
	}
	clean := ansi.Strip(pane)
	if hold := g.engine.TypingHold(target.Tool, clean); hold != "" {
		return hold, nil
	}
	state, _ := g.engine.Match(target.Tool, clean)
	if state != status.Idle {
		return state, nil
	}
	caretX, caretY, err := g.tmux.Cursor(target.ID)
	if err != nil {
		return "", err
	}
	rows := strings.Split(clean, "\n")
	if caretY >= 0 && caretY < len(rows) && textBeforeCaret(g.engine, target.Tool, rows[caretY], caretX) {
		return namesweep.StateTyping, nil
	}
	return status.Idle, nil
}

func (g sweepGate) Cache(target namesweep.Verdict) promptcache.State {
	return g.cache.Lookup(target.Cwd, target.AgentSessionID)
}

func (m *Model) handleNameSweepKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	action, bound := m.action(keymap.ContextNameSweep, msg)
	if !bound {
		return m, nil
	}
	switch action {
	case keymap.Cancel:
		if m.nameSweep.sending {
			if m.nameSweep.stop != nil {
				close(m.nameSweep.stop)
				m.nameSweep.stop = nil
			}
			return m, nil
		}
		m.nameSweep = nameSweepState{}
		m.mode = modeList
		return m, nil
	case keymap.CursorUp:
		m.nameSweep.scroll = max(m.nameSweep.scroll-1, 0)
		return m, nil
	case keymap.CursorDown:
		m.nameSweep.scroll = min(m.nameSweep.scroll+1, m.nameSweepScrollLimit())
		return m, nil
	case keymap.PageUp:
		m.nameSweep.scroll = max(m.nameSweep.scroll-m.nameSweepPage(), 0)
		return m, nil
	case keymap.PageDown:
		m.nameSweep.scroll = min(m.nameSweep.scroll+m.nameSweepPage(), m.nameSweepScrollLimit())
		return m, nil
	case keymap.Confirm:
		if m.nameSweep.building || m.nameSweep.sending {
			return m, nil
		}
		if m.nameSweep.result != nil {
			m.nameSweep = nameSweepState{}
			m.mode = modeList
			return m, nil
		}
		if len(m.nameSweep.plan.Targets) == 0 {
			return m, nil
		}
		return m, m.sendNameSweep()
	}
	return m, nil
}

func (m *Model) applyNameSweepPlan(msg nameSweepPlanMsg) {
	m.nameSweep.building = false
	m.nameSweep.plan = msg.plan
}

func (m *Model) applyNameSweepResult(msg nameSweepDoneMsg) {
	result := msg.result
	m.nameSweep.sending = false
	m.nameSweep.stop = nil
	m.nameSweep.result = &result
	m.nameSweep.scroll = 0
}

// nameSweepBodyRoom is the rows of plan the card can show, with its own
// chrome and the error row taken off the terminal height first.
func (m *Model) nameSweepBodyRoom() int {
	inner := cardInnerWidth(helpCardWidth(m.width))
	room := m.height - 5 - lipgloss.Height(legendInline(m.nameSweepHint(), inner))
	if m.errBar.text != "" {
		room -= 2
	}
	return max(room, 1)
}

func (m *Model) nameSweepPage() int {
	return max(m.nameSweepBodyRoom()-1, 1)
}

func (m *Model) nameSweepScrollLimit() int {
	return max(0, len(m.nameSweepBody(cardInnerWidth(helpCardWidth(m.width))))-m.nameSweepBodyRoom())
}

func (m *Model) nameSweepHint() [][2]string {
	switch {
	case m.nameSweep.building:
		return [][2]string{{"esc", "cancel"}}
	case m.nameSweep.sending:
		return [][2]string{{"esc", "stop"}}
	case m.nameSweep.result != nil:
		return [][2]string{{"↵/esc", "close"}}
	case len(m.nameSweep.plan.Targets) == 0:
		return [][2]string{{"esc", "close"}}
	}
	return [][2]string{{"↑↓", "scroll"}, {"y/↵", "send"}, {"n/esc", "cancel"}}
}

func (m *Model) nameSweepTitle() string {
	if m.nameSweep.result != nil {
		return "✎ Name sweep — done"
	}
	if m.nameSweep.sending {
		return "✎ Name sweep — sending"
	}
	return "✎ Name sweep — dry run"
}

func (m *Model) viewNameSweep() string {
	width := helpCardWidth(m.width)
	inner := cardInnerWidth(width)
	body := fitBody(m.nameSweepBody(inner), m.nameSweepBodyRoom(), m.nameSweep.scroll)
	return m.cardSized(width, m.nameSweepTitle(), strings.Join(body, "\n"), m.nameSweepHint())
}

// nameSweepBody renders the plan: what would be sent, what would not and why,
// and the message itself. The skips send nothing, but they are the half an
// operator most needs to read, so they are never folded away.
func (m *Model) nameSweepBody(inner int) []string {
	if m.nameSweep.building {
		return []string{subtleStyle.Render("reading transcripts…")}
	}
	if m.nameSweep.result != nil {
		return m.nameSweepResultBody(inner)
	}
	plan := m.nameSweep.plan
	lines := []string{
		subtleStyle.Render(fmt.Sprintf(
			"adopted panes only · %d managed, archived or shell %s not in scope",
			plan.OutOfScope, plural(plan.OutOfScope, "row is", "rows are"))),
		"",
	}
	lines = append(lines, sectionHead("send", len(plan.Targets), colorAccent))
	if len(plan.Targets) == 0 {
		lines = append(lines, "  "+subtleStyle.Render("nothing is both idle and warm right now"))
	}
	for _, target := range plan.Targets {
		lines = append(lines, sweepRow("✓", target, inner, valueStyle))
	}
	lines = append(lines, "", sectionHead("hold", len(plan.Skipped), colorWaiting))
	for _, skipped := range plan.Skipped {
		lines = append(lines, sweepRow("✗", skipped, inner, mutedStyle))
	}
	if len(plan.Targets) > 0 {
		lines = append(lines, "", subtleStyle.Render("typed into each pane above, one line, session id differs per pane:"))
		for _, line := range strings.Split(ansi.Wordwrap(plan.Targets[0].Directive, inner-2, "-"), "\n") {
			lines = append(lines, "  "+mutedStyle.Render(line))
		}
	}
	return lines
}

func (m *Model) nameSweepResultBody(inner int) []string {
	result := m.nameSweep.result
	var lines []string
	if result.Stopped {
		lines = append(lines, errStyle.Render("stopped part way through"), "")
	}
	if result.Err != nil {
		lines = append(lines, errStyle.Render("send failed: "+result.Err.Error()), "")
	}
	lines = append(lines, sectionHead("sent", len(result.Sent), colorAccent))
	for _, sent := range result.Sent {
		lines = append(lines, sweepRow("✓", sent, inner, valueStyle))
	}
	if len(result.Held) > 0 {
		lines = append(lines, "", sectionHead("held at the door", len(result.Held), colorWaiting))
		for _, held := range result.Held {
			lines = append(lines, sweepRow("✗", held, inner, mutedStyle))
		}
	}
	return lines
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func sectionHead(label string, count int, tone color.Color) string {
	return lipgloss.NewStyle().Foreground(tone).Bold(true).Render(label) +
		subtleStyle.Render(fmt.Sprintf(" · %d", count))
}

// sweepNameColumn is how much of a session name a row shows before the
// reason, which is the part that decides anything, gets the rest.
const sweepNameColumn = 22

func sweepRow(mark string, verdict namesweep.Verdict, inner int, nameStyle fastStyle) string {
	name := padRight(nameStyle.Render(cellTruncate(verdict.Name, sweepNameColumn-1, "…")), sweepNameColumn)
	reason := verdict.Reason
	if room := inner - sweepNameColumn - 4; room > 8 {
		reason = cellTruncate(reason, room, "…")
	}
	return "  " + mark + " " + name + subtleStyle.Render(reason)
}

func nameSweepSummary(result namesweep.Result) string {
	summary := fmt.Sprintf("name sweep: asked %d, held %d", len(result.Sent), len(result.Held))
	if result.Stopped {
		summary += " (stopped)"
	}
	return summary
}
