// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// paneFacts is what tmux says about the focused pane besides its text: where
// the caret sits, whether the pane's application has claimed the mouse, and
// how far its history goes.
//
// The caret matters because a capture carries none, and a terminal with no
// visible cursor gives no sense of where typing will land. The mouse flags
// matter because the whole wheel-and-click hand-off is routed off them: a
// pane whose agent tracks the mouse gets the report forwarded, one that does
// not gets the manager's own scrollback.
type paneFacts struct {
	cursorX  int
	cursorY  int
	cursorOK bool

	// paneMotion narrows mouse ownership to the applications that asked for
	// every pointer move rather than only clicks and drags, and paneSGR to
	// those that asked for the modern report encoding.
	paneMouse   bool
	paneMotion  bool
	paneSGR     bool
	historySize int

	// activity is tmux's #{window_activity}: the unix second the pane last
	// produced output in. tmux stamps it from input_parse_buffer, so it
	// moves for every batch of bytes the pane's application writes and for
	// nothing else. It rides this read rather than a fork, which is what
	// lets the focused tick ask whether a capture is worth forking at all --
	// see shouldCapture. activityOK is false when tmux answered without it.
	activity   int64
	activityOK bool
}

// paneStateFormat is the display-message format applyPaneState parses.
const paneStateFormat = "#{cursor_x},#{cursor_y},#{mouse_any_flag}#{mouse_button_flag}#{mouse_standard_flag},#{history_size},#{mouse_all_flag},#{mouse_sgr_flag},#{window_activity}"

// paneStateMsg carries one read of that format back to the event loop.
type paneStateMsg struct {
	sessID string
	state  string
}

// paneStateCmd reads one focused session's pane facts off the event loop.
// It rides the pooled control pipe like every other focused read -- see
// tmux/pipe.go -- so asking costs tens of microseconds rather than a fork.
func (m *Model) paneStateCmd(sessID string) tea.Cmd {
	driver := m.tmux
	if driver == nil || sessID == "" {
		return nil
	}
	return func() tea.Msg {
		state, err := driver.PaneState(sessID, paneStateFormat)
		if err != nil {
			return nil
		}
		return paneStateMsg{sessID: sessID, state: state}
	}
}

// applyPaneState reads "x,y,mouseflags,history,allmotion,sgr,activity" from
// display-message. A malformed reply leaves the zero values: no cursor, no
// mouse claim, no history, and no activity stamp -- which reads as "cannot
// tell", so the tick forks its capture rather than trusting a stamp it did
// not get.
func applyPaneState(facts *paneFacts, reply string) {
	parts := strings.Split(strings.TrimSpace(reply), ",")
	if len(parts) != 7 {
		return
	}
	x, errX := strconv.Atoi(strings.TrimSpace(parts[0]))
	y, errY := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errX == nil && errY == nil {
		facts.cursorX, facts.cursorY, facts.cursorOK = x, y, true
	}
	facts.paneMouse = strings.Contains(parts[2], "1")
	if size, err := strconv.Atoi(strings.TrimSpace(parts[3])); err == nil && size > 0 {
		facts.historySize = size
	}
	facts.paneMotion = strings.TrimSpace(parts[4]) == "1"
	facts.paneSGR = strings.TrimSpace(parts[5]) == "1"
	if stamp, err := strconv.ParseInt(strings.TrimSpace(parts[6]), 10, 64); err == nil && stamp > 0 {
		facts.activity, facts.activityOK = stamp, true
	}
}
