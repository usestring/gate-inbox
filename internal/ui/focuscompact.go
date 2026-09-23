package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// resizeSettleDelay is how long a terminal size has to hold before the
// sessions are resized to it. A phone keyboard opening or closing, and a
// window being dragged, each arrive as a burst of sizes; every one used to
// cost the agent a transcript redraw.
const resizeSettleDelay = 300 * time.Millisecond

// resizeSettleMsg fires when a size has held for resizeSettleDelay.
type resizeSettleMsg struct{ seq int }

// settleResizeLater arms the resize timer for the size the model just
// took. The manager's own frame repaints at once; only the per-session
// tmux resizes wait for the size to hold.
func (m *Model) settleResizeLater() tea.Cmd {
	m.resizeSeq++
	seq := m.resizeSeq
	return tea.Tick(resizeSettleDelay, func(time.Time) tea.Msg { return resizeSettleMsg{seq: seq} })
}
