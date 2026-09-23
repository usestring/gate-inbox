package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	legendPeekTapWindow     = 500 * time.Millisecond
	legendPeekReleaseWindow = 140 * time.Millisecond
)

type legendPeekState struct {
	visible  bool
	repeated bool
	sticky   bool
	seq      uint64
}

type legendPeekDecayMsg struct{ seq uint64 }

func (m *Model) beginLegendPeek() tea.Cmd {
	m.legendPeek.visible = true
	m.legendPeek.repeated = false
	m.legendPeek.sticky = false
	return m.armLegendPeek(legendPeekTapWindow)
}

func (m *Model) repeatLegendPeek() tea.Cmd {
	m.legendPeek.repeated = true
	return m.armLegendPeek(legendPeekReleaseWindow)
}

func (m *Model) armLegendPeek(after time.Duration) tea.Cmd {
	m.legendPeek.seq++
	seq := m.legendPeek.seq
	return tea.Tick(after, func(time.Time) tea.Msg { return legendPeekDecayMsg{seq: seq} })
}

func (m *Model) dismissLegendPeek() {
	m.legendPeek = legendPeekState{seq: m.legendPeek.seq + 1}
}

func (m *Model) settleLegendPeek(msg legendPeekDecayMsg) {
	if !m.legendPeek.visible || msg.seq != m.legendPeek.seq {
		return
	}
	if m.legendPeek.repeated {
		m.dismissLegendPeek()
		return
	}
	m.legendPeek.sticky = true
}

func (m *Model) overlayLegendPeek(frame string, bodyHeight int) string {
	if !m.legendPeek.visible || bodyHeight < 1 {
		return frame
	}
	overlay := m.peekLegend(bodyHeight)
	if overlay == "" {
		return frame
	}
	rows := strings.Split(frame, "\n")
	peekRows := strings.Split(overlay, "\n")
	start := m.listChromeRows() + bodyHeight - len(peekRows)
	if start < m.listChromeRows() {
		start = m.listChromeRows()
	}
	for i, row := range peekRows {
		at := start + i
		if at >= 0 && at < len(rows) {
			rows[at] = paint(row, m.width, blockHex())
		}
	}
	return strings.Join(rows, "\n")
}

func (m *Model) peekLegend(maxRows int) string {
	return legendBar(m.peekLegendSections(), m.width, maxRows)
}
