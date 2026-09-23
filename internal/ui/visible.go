package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Whether the operator is actually looking at the manager.
//
// It matters because of what the preview costs the rest of the board. Showing
// a pane means holding its tmux window at the preview panel's size, and that
// is a pin on a window the operator also uses directly. A manager sitting in
// a background tmux window has no reader and no business holding anything.
//
// Two signals, because neither is sufficient alone.
//
// The terminal's own focus reporting is the precise one: the emulator says
// when its window gains and loses focus, and Bubble Tea turns that into
// FocusMsg and BlurMsg. It arrives instantly and costs nothing. But inside
// tmux it is gated on the server option `focus-events`, which is off by
// default (verified on tmux 3.4) -- and the manager runs inside the
// operator's own tmux, on a server it does not own. Turning that option on
// would be a server-global write on somebody else's server, which is exactly
// the class of change the manager is not allowed to make.
//
// So there is a second signal that needs no write at all: ask tmux where the
// operator's client actually is. Three fields off the manager's own pane
// answer it, and the answer is unambiguous:
//
//	pane_active=1 window_active=1 session_attached=1  -> being looked at
//	pane_active=1 window_active=0 session_attached=1  -> another window
//	pane_active=1 window_active=1 session_attached=0  -> fully detached
//
// That read rides the pooled control pipe at tens of microseconds and runs on
// the poll cadence, so a release can land up to one pass late. Late is the
// right trade: a window unpinned a second after the operator looked away is a
// window they get back, and the failure this replaces was one they never got
// back at all.
//
// A manager running outside tmux has no pane to ask about and falls through
// to focus reporting alone, which works properly there.

// visibleFormat is the display-message format applyVisible parses.
const visibleFormat = "#{pane_active},#{window_active},#{session_attached}"

// visibleMsg carries one read of it back to the event loop.
type visibleMsg struct {
	state     string
	device    string
	deviceErr error
}

// ownPaneVisibleCmd asks tmux whether the manager's own pane is the one the
// operator's client is currently showing. It returns nil when the manager is
// not running inside a tmux pane, which is what makes this a supplement to
// focus reporting rather than a replacement for it.
func (m *Model) ownPaneVisibleCmd() tea.Cmd {
	driver := m.tmux
	pane := m.ownPane
	if driver == nil || pane == "" {
		return nil
	}
	return func() tea.Msg {
		state, err := driver.PaneStateAt(m.ownSocket, pane, visibleFormat)
		if err != nil {
			// A pane the manager cannot ask about is not evidence that
			// nobody is watching, and treating it as a blur would release
			// the pin the preview on screen is relying on.
			return nil
		}
		device, deviceErr := driver.ClientDeviceAt(m.ownSocket, pane)
		return visibleMsg{state: state, device: device, deviceErr: deviceErr}
	}
}

// applyVisible reads "pane_active,window_active,session_attached". Anything
// it cannot parse leaves visibility alone for the reason above.
func applyVisible(reply string) (visible bool, ok bool) {
	parts := strings.Split(strings.TrimSpace(reply), ",")
	if len(parts) != 3 {
		return false, false
	}
	for _, part := range parts[:2] {
		if strings.TrimSpace(part) != "1" {
			return false, true
		}
	}
	// session_attached is a count of clients, so anything but zero means
	// somebody has the session open.
	attached := strings.TrimSpace(parts[2])
	return attached != "" && attached != "0", true
}

// setVisible records whether the operator is looking, and asks for the pins
// to be reconsidered when that changed. Releasing on blur and re-pinning on
// focus is at most two tmux writes, because at most one window is ever
// pinned.
func (m *Model) setVisible(visible bool) tea.Cmd {
	if m.visible == visible {
		return nil
	}
	m.visible = visible
	return m.resizeSessions()
}
