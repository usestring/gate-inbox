package ui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/tmux"
)

// The board draws links, copies URLs and forwards mouse notches through a
// tmux it does not own, and when that tmux is configured to drop any of it
// the loss is silent: a link renders as its own label, a copied URL never
// reaches the clipboard, and nothing anywhere says a setting is why. The
// operator whose ~/.tmux.conf carries the lines sees a board where links
// work; the one without them sees the same board and reports that they do
// not. This is the manager saying which line closes the gap, once, at
// startup, on the terminal the operator is actually sitting at.
//
// It is a card rather than a status line because the answer is a line of
// config to copy, and it is raised once per set of findings rather than
// every start: a setting the operator has read about and chosen to leave
// alone must not greet them every time they open the board.

// tmuxHintSeenSetting holds the finding keys already shown, so a config that
// grows a new problem is raised again while a known one stays quiet.
const tmuxHintSeenSetting = "tmux_config_seen"

type tmuxHintState struct {
	findings []tmux.ConfigFinding
}

// tmuxConfigMsg carries the startup read back to the event loop. The read
// forks a tmux, so it happens off the loop like every other one.
type tmuxConfigMsg struct {
	findings []tmux.ConfigFinding
}

func (m *Model) checkTmuxConfig() tea.Msg {
	return tmuxConfigMsg{findings: m.tmux.CheckConfig()}
}

// maybeOpenTmuxHint raises the card for findings this install has not been
// shown. Init arms it, so a Model built directly never raises one.
func (m *Model) maybeOpenTmuxHint(findings []tmux.ConfigFinding) {
	// modeList alone: the welcome card takes a first run's screen, and a
	// config note must not land over whatever the operator opened the
	// manager to do. The read happens once a run, so a note deferred here is
	// not raised again in this one -- nothing is marked seen, and the next
	// start offers it.
	if !m.tmuxHintArmed || m.store == nil || m.mode != modeList || len(findings) == 0 {
		return
	}
	m.tmuxHintArmed = false
	seen, err := m.store.Setting(tmuxHintSeenSetting)
	if err != nil {
		// An unreadable settings row is not a reason to nag: a card raised
		// on every start is worse than one the operator has already read.
		return
	}
	if !anyUnseen(findings, seen) {
		return
	}
	if err := m.store.SetSetting(tmuxHintSeenSetting, seenKeys(findings, seen)); err != nil {
		m.errBar.text = "saving the tmux config note: " + err.Error()
	}
	// The whole set is shown, not just what is new: the fresh finding is why
	// the card is up, and the rest is the same page of settings to fix while
	// the operator is in ~/.tmux.conf anyway.
	m.tmuxHint = tmuxHintState{findings: findings}
	m.mode = modeTmuxHint
}

// anyUnseen is what makes the card worth raising: everything already recorded
// has been put in front of this operator once.
func anyUnseen(findings []tmux.ConfigFinding, seen string) bool {
	known := map[string]bool{}
	for _, key := range strings.Split(seen, ",") {
		known[strings.TrimSpace(key)] = true
	}
	for _, finding := range findings {
		if !known[finding.Key] {
			return true
		}
	}
	return false
}

// seenKeys is the recorded set with this pass folded in. Keys are kept rather
// than replaced, so a setting fixed and later undone is not raised twice.
func seenKeys(findings []tmux.ConfigFinding, seen string) string {
	keys := map[string]bool{}
	for _, key := range strings.Split(seen, ",") {
		if key = strings.TrimSpace(key); key != "" {
			keys[key] = true
		}
	}
	for _, finding := range findings {
		keys[finding.Key] = true
	}
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// Any key closes it, the way the launch hint does: there is nothing to decide
// here, only a line to read and copy.
func (m *Model) handleTmuxHintKey(tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.tmuxHint = tmuxHintState{}
	m.mode = modeList
	return m, nil
}

// tmuxHintLines renders each fix on its own row and unwrapped: it is there to
// be typed or copied, and a wrapped config line copies wrong.
func tmuxHintLines(findings []tmux.ConfigFinding, inner int) []string {
	var lines []string
	for i, finding := range findings {
		if i > 0 {
			lines = append(lines, "")
		}
		for _, line := range strings.Split(ansi.Wordwrap(finding.Detail, inner, "-"), "\n") {
			lines = append(lines, mutedStyle.Render(line))
		}
		if finding.Fix != "" {
			lines = append(lines, keyStyle.Render(cellTruncate(finding.Fix, inner, "…")))
		}
	}
	if !anyFix(findings) {
		return lines
	}
	lines = append(lines, "")
	for _, line := range strings.Split(ansi.Wordwrap(
		"Those lines go in ~/.tmux.conf. `tmux source-file ~/.tmux.conf` applies them to the "+
			"client you are on now -- no reattach needed.", inner, "-"), "\n") {
		lines = append(lines, subtleStyle.Render(line))
	}
	return lines
}

func anyFix(findings []tmux.ConfigFinding) bool {
	for _, finding := range findings {
		if finding.Fix != "" {
			return true
		}
	}
	return false
}

func (m *Model) viewTmuxHint() string {
	width := m.cardWidth()
	body := tmuxHintLines(m.tmuxHint.findings, cardInnerWidth(width))
	return m.cardSized(width, "◈ tmux is dropping features this board uses",
		strings.Join(body, "\n"), [][2]string{{"any key", "close"}})
}
