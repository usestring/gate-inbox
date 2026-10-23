package ui

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/extension/textfmt"
)

// headroomInterval is how often the rail asks the build how much subscription
// quota each CLI has left. The reporter answers from its own cache, so this
// only bounds how stale the dock can be.
const headroomInterval = time.Minute

type headroomTickMsg struct{}

// headroomMsg is one summary line per CLI the build could read.
type headroomMsg struct {
	quotas map[string]string
}

func headroomTick() tea.Cmd {
	return tea.Tick(headroomInterval, func(time.Time) tea.Msg { return headroomTickMsg{} })
}

// headroomStart reads now and keeps reading on the interval. A build whose
// chooser cannot report quota asks nothing, ever.
func (m *Model) headroomStart() tea.Cmd {
	if m.headroomReporter() == nil {
		return nil
	}
	return tea.Batch(m.headroomCmd(), headroomTick())
}

func (m *Model) headroomReporter() extension.ToolQuotaReporter {
	chooser, ok := toolChooser()
	if !ok {
		return nil
	}
	reporter, _ := chooser.(extension.ToolQuotaReporter)
	return reporter
}

// headroomCmd reads the CLI names on the event loop and asks the reporter off
// it.
func (m *Model) headroomCmd() tea.Cmd {
	reporter := m.headroomReporter()
	if reporter == nil {
		return nil
	}
	names := m.enabledToolNames()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return headroomMsg{quotas: reporter.ToolQuotaSummaries(ctx, names)}
	}
}

// headroomLines is the subscription block under the machine meters, one line
// per CLI with a reading, or nothing when no CLI has one.
func (m *Model) headroomLines(width int) []string {
	if len(m.headroom) == 0 {
		return nil
	}
	pad := spaces(railInset)
	lines := []string{pad + subtleStyle.Render("subscription")}
	for _, name := range slices.Sorted(maps.Keys(m.headroom)) {
		label := textfmt.TruncateWidth(name, 9, "…")
		lines = append(lines, pad+labelStyle.Width(9).Render(label)+valueStyle.Render(textfmt.TruncateWidth(m.headroom[name], max(width-railInset-9, 1), "…")))
	}
	return lines
}

// headroomKey is the block's readings in a comparable form for meterKey.
func (m *Model) headroomKey() string {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(m.headroom)) {
		b.WriteString(name + "\x00" + m.headroom[name] + "\n")
	}
	return b.String()
}
