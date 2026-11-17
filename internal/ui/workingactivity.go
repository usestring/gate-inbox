package ui

import (
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const workingActivitySetting = "working_activity"
const activityScrollInterval = 200 * time.Millisecond

type workingActivity struct {
	text  string
	since time.Time
}

type activityTickMsg struct{}

func storedWorkingActivity(st *store.Store) bool {
	chosen, err := st.Setting(workingActivitySetting)
	return err == nil && chosen == "scroll"
}

func workingActivityLabel(on bool) string {
	if on {
		return "scroll"
	}
	return "off"
}

func activityExcerpt(region string) string {
	lines := strings.Split(region, "\n")
	var tail []string
	for i := len(lines) - 1; i >= 0 && len(tail) < 4; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.Trim(line, "─━═-_") == "" ||
			strings.Contains(line, "esc to interrupt") || strings.Contains(line, "esc interrupt") ||
			strings.Contains(line, "esc to cancel") {
			continue
		}
		tail = append(tail, line)
	}
	slices.Reverse(tail)
	return textfmt.OneLine(textfmt.StripControl(strings.Join(tail, " · ")))
}

func (m *Model) updateWorkingActivity(texts map[string]string) {
	updated := make(map[string]workingActivity, len(texts))
	now := time.Now()
	for _, sess := range m.sessions {
		text := texts[sess.ID]
		if sess.Status != status.Working || text == "" {
			continue
		}
		activity := m.workingActivities[sess.ID]
		if activity.text != text {
			activity = workingActivity{text: text, since: now}
		}
		updated[sess.ID] = activity
	}
	m.workingActivities = updated
}

func (m *Model) rowWorkingActivity(entry treeRow) (workingActivity, bool) {
	activity := m.workingActivities[entry.sess.ID]
	return activity, m.workingScroll && entry.sess.Status == status.Working && activity.text != "" && !m.renamingRow(entry)
}

func (m *Model) startActivityTick() tea.Cmd {
	if m.activityAnimating || !m.workingScroll || len(m.workingActivities) == 0 {
		return nil
	}
	m.activityAnimating = true
	return activityTick()
}

func activityTick() tea.Cmd {
	return tea.Tick(activityScrollInterval, func(time.Time) tea.Msg { return activityTickMsg{} })
}

func scrollActivity(text string, room int, elapsed time.Duration) string {
	width := textfmt.Width(text)
	if room <= 0 {
		return ""
	}
	if width <= room {
		return text
	}
	const pauseSteps = 6
	travel := width - room
	step := int(max(elapsed, 0)/activityScrollInterval) % (travel + 2*pauseSteps)
	offset := min(max(step-pauseSteps, 0), travel)
	return ansi.Cut(text, offset, offset+room)
}
