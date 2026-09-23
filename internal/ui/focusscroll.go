// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
)

// focusScrollStep is how many lines one wheel notch or one alt+arrow moves
// the focused pane through its history. It is fixed rather than a share of
// the pane: a trackpad swipe or a phone flick arrives as a burst of
// notches, and a step that grew with the terminal turned one flick on a
// tall screen into a jump of a hundred lines.
const focusScrollStep = 3

// focusReadStale is how long a region read may be out before another notch
// may start one anyway. It is an escape hatch, not a timeout: the read still
// lands if it ever comes back, and the offset it was for is checked before
// anything is painted. A dead wheel is a worse failure than one extra
// capture a second.
const focusReadStale = time.Second

// focusScrollKind names a keyboard scroll gesture over the focused pane.
type focusScrollKind int

const (
	focusScrollUp focusScrollKind = iota
	focusScrollDown
	focusScrollPageUp
	focusScrollPageDown
	focusScrollTop
	focusScrollBottom
)

// scrollKindOf turns a scroll action into the gesture it stands for. The
// keys themselves live in the key map now -- they are all alt-modified by
// default for the same reason alt+o and alt+x are, since every unmodified
// key belongs to the agent and PgUp or the arrows drive its own prompt --
// and this is the one place the six actions are read as one family.
func scrollKindOf(action keymap.Action) focusScrollKind {
	switch action {
	case keymap.PreviewUp:
		return focusScrollUp
	case keymap.PreviewDown:
		return focusScrollDown
	case keymap.PreviewPageUp:
		return focusScrollPageUp
	case keymap.PreviewPageDown:
		return focusScrollPageDown
	case keymap.PreviewTop:
		return focusScrollTop
	}
	return focusScrollBottom
}

// isScrollAction reports whether an action is one of the six.
func isScrollAction(action keymap.Action) bool {
	switch action {
	case keymap.PreviewUp, keymap.PreviewDown, keymap.PreviewPageUp,
		keymap.PreviewPageDown, keymap.PreviewTop, keymap.PreviewBottom:
		return true
	}
	return false
}

// focusScrollMsg carries the capture of one scrolled-back region of the
// focused pane's history. offset, rows and width are the target it was
// asked for, so a frame whose target moved while it was in flight can be
// recognised and re-read rather than painted.
type focusScrollMsg struct {
	sessID string
	offset int
	rows   int
	width  int
	block  string
	ok     bool
}

// Mouse button codes as the reports carry them: the two wheel
// directions, and a pointer move with no button held (button 3 with the
// motion bit set).
const (
	wheelUpButton   = 64
	wheelDownButton = 65
	motionButton    = 35
	leftButton      = 0
	middleButton    = 1
	rightButton     = 2
	motionBit       = 32
)

// x10Limit is the highest cell the original encoding can name: each
// coordinate is one byte biased by 32, so it runs out at 223.
const x10Limit = 223

// sgrMouse is one mouse report in SGR encoding at a one-based pane cell.
func sgrMouse(button, col, row int) string {
	return fmt.Sprintf("\x1b[<%d;%d;%dM", button, col+1, row+1)
}

// x10Mouse is one mouse report in the original encoding, for an app that
// tracks the mouse without asking for SGR. Not ok past the cell the
// encoding can name, where the report would land on the wrong column.
func x10Mouse(button, col, row int) (string, bool) {
	if col >= x10Limit || row >= x10Limit {
		return "", false
	}
	return string([]byte{0x1b, '[', 'M', byte(32 + button), byte(33 + col), byte(33 + row)}), true
}

// hexBytes renders a string as the space-separated byte codes send-keys -H
// takes, which sidesteps tmux quoting for control sequences entirely.
func hexBytes(s string) string {
	codes := make([]string, 0, len(s))
	for i := 0; i < len(s); i++ {
		codes = append(codes, fmt.Sprintf("%02x", s[i]))
	}
	return strings.Join(codes, " ")
}

// wheelReport is what one wheel notch looks like on the wire for this
// pane: the notch itself, in the encoding the pane asked for, behind a
// pointer move when the app tracks all motion. An app tracking all
// motion places the wheel by where the pointer last moved, and focus
// mode keeps every move for its own selection, so without the move the
// notch arrives with the pointer wherever the app last saw it.
func (m *Model) wheelReport(up bool, col, row int) (string, bool) {
	button := wheelDownButton
	if up {
		button = wheelUpButton
	}
	return m.mouseReport(button, false, col, row)
}

// mouseReport encodes one press, drag, release or wheel event for the
// focused pane. An all-motion app needs a pointer move ahead of discrete
// events because focus mode does not pass its ordinary pointer moves on.
func (m *Model) mouseReport(button int, release bool, col, row int) (string, bool) {
	if m.pane.sgr {
		terminator := "M"
		if release {
			terminator = "m"
		}
		report := fmt.Sprintf("\x1b[<%d;%d;%d%s", button, col+1, row+1, terminator)
		if m.pane.motion && !release {
			report = sgrMouse(motionButton, col, row) + report
		}
		return report, true
	}
	if release {
		// X10 cannot name the released button: button 3 denotes all releases.
		button = 3
	}
	report, ok := x10Mouse(button, col, row)
	if !ok {
		return "", false
	}
	if m.pane.motion && !release {
		move, moveOK := x10Mouse(motionButton, col, row)
		if !moveOK {
			return "", false
		}
		report = move + report
	}
	return report, true
}

// wheelFocus routes one wheel notch at the pane on screen -- the focused
// one, or the list's preview of the selected session. An application that
// has turned on mouse tracking scrolls itself and gets the event; anything
// else is a plain pane, so the wheel walks tmux's own scrollback instead.
// Agent CLIs are the first case: they run on the alternate screen, where
// tmux keeps no history at all, and do their own scrolling.
func (m *Model) wheelFocus(up bool, x, y int) tea.Cmd {
	if m.showsConversation() {
		if up {
			return m.scrollConversation(-focusScrollStep)
		}
		return m.scrollConversation(focusScrollStep)
	}
	if m.mode != modeFocus && m.mode != modeList {
		return nil
	}
	if m.pane.mouse {
		// A notch landing on the chrome around the pane still means "scroll
		// the pane": in focus nothing else scrolls, and on a short terminal
		// the chrome is most of the screen.
		row, col, ok := m.nearestPaneCell(x, y)
		if !ok {
			return nil
		}
		return m.forwardWheel(up, row, col, 1)
	}
	delta := 1
	if up {
		delta = -1
	}
	return m.scrollFocus(delta)
}

// nearestPaneCell is paneCell for a pointer that may sit outside the pane:
// the pointer is pulled to the nearest cell inside the box.
func (m *Model) nearestPaneCell(x, y int) (row, col int, ok bool) {
	box := m.pane.box
	if !box.ok || box.width <= 0 || box.height <= 0 {
		return 0, 0, false
	}
	x = min(max(x, box.x), box.x+box.width-1)
	y = min(max(y, box.y), box.y+box.height-1)
	return y - box.y, x - box.x, true
}

// forwardWheel sends notches wheel reports at a pane cell to an application
// that owns the mouse, which is how the manager scrolls a pane it cannot
// scroll itself. One write carries them all: a page is a dozen notches, and
// a dozen round trips down the pipe is a stall the operator can feel.
func (m *Model) forwardWheel(up bool, row, col, notches int) tea.Cmd {
	sess, ok := m.selected()
	if !ok || m.tmux == nil {
		return nil
	}
	report, reportOK := m.wheelReport(up, col, m.wheelTargetRow(sess.Tool, row+m.paneRowOffset(m.pane.box.height)))
	if !reportOK {
		return nil
	}
	// Every notch is sent -- the pane owes the operator the distance they
	// asked for -- but only one chase looks for the result. A chase is a loop
	// of forked captures, and a flick is dozens of notches: one chase each is
	// the same fork storm the region reads made, and the frames it would
	// bring back are frames the chase already out will bring back anyway.
	// A notch paints nothing itself: the frame it provokes arrives as a
	// capture, and until then the screen is right as it stands. An error
	// from the send is the one thing it can put on screen.
	errText := m.errBar.text
	defer func() {
		if m.errBar.text == errText {
			m.frameUnchanged()
		}
	}()
	if m.focusChasing && time.Since(m.focusChasingAt) < focusCaptureStale {
		m.sendFocusReport(strings.Repeat(report, max(notches, 1)))
		m.noteFocusWheel()
		return nil
	}
	// Read before the send: a baseline taken after it could already hold the
	// repaint the chase is there to recognise, which is the same rule the
	// keystroke path's baseline follows. The frame on screen is that read
	// when it is fresh enough: a flick lands a capture every few
	// milliseconds, and each one is a real read of this pane taken before
	// this notch went out. What the keystroke path guards against -- a
	// frame left stale by a resize or a poll at another size -- is what the
	// freshness bound rules out, and a wrong guess costs one early frame
	// that the next capture corrects, not a fork per notch.
	baseline, ok := m.freshBaseline(sess.ID)
	if !ok {
		baseline, _ = m.tmux.CapturePane(sess.ID)
	}
	m.sendFocusReport(strings.Repeat(report, max(notches, 1)))
	m.noteFocusWheel()
	return m.startChase(sess, baseline)
}

// wheelBaselineFresh is how recently the frame on screen must have been
// captured to stand in for a fresh read as a chase's baseline. It is a
// little over one streaming tick, so a flick that is being followed at that
// rate never forks for a baseline, and a pane left alone for longer is read
// afresh the way a keystroke reads it.
const wheelBaselineFresh = 60 * time.Millisecond

// freshBaseline is the frame on screen when it was captured from this pane,
// live at the bottom, within wheelBaselineFresh.
func (m *Model) freshBaseline(sessID string) (string, bool) {
	if m.pane.forID != sessID || m.scrolledBack() || m.previewAt.IsZero() ||
		time.Since(m.previewAt) > wheelBaselineFresh {
		return "", false
	}
	return m.preview, true
}

// wheelTargetRow keeps a forwarded notch off the tool's own input rows. An
// application that routes the wheel by hover swallows a notch landing on its
// composer: opencode scrolls its messages when the notch arrives over them
// and drops it over its ┃ input box, so on a phone-sized pane -- where the
// box is much of the screen -- swipe-to-scroll reads as dead, while a tool
// that scrolls wherever the notch lands keeps working. The notch is walked
// up past the box, and past the blank gap a tool leaves between its transcript
// and its composer (a notch on empty space hits no element either), to the
// first row with content above. Clicks keep their exact cell, since pressing
// the composer to type in it is legitimate; only the wheel moves.
func (m *Model) wheelTargetRow(tool string, paneRow int) int {
	if m.engine == nil {
		return paneRow
	}
	rows := strings.Split(strings.TrimSuffix(m.preview, "\n"), "\n")
	for paneRow > 0 && paneRow < len(rows) {
		stripped := ansi.Strip(rows[paneRow])
		if _, ok := m.engine.InputPrefix(tool, stripped); !ok && strings.TrimSpace(stripped) != "" {
			break
		}
		paneRow--
	}
	return paneRow
}

// keyScrollFocus is the keyboard's way through the focused pane's history,
// for a client with no wheel to turn: a phone, or a terminal that sends a
// swipe as arrow keys. A plain pane walks tmux's scrollback the way a notch
// does; an application that owns the mouse is handed the notches instead,
// at the middle of the pane, so it scrolls its own viewport.
func (m *Model) keyScrollFocus(kind focusScrollKind) tea.Cmd {
	if m.mode != modeFocus && m.mode != modeList {
		return nil
	}
	rows := m.previewPaneHeight()
	// A whole screen's worth stands in for "everything" on a pane whose
	// history the manager cannot see: an app already at its edge ignores
	// the surplus notches.
	whole := max(m.pane.history, m.focusScroll, rows*focusScrollStep)
	if m.showsConversation() {
		whole = len(m.conversation.wrapped(m.previewPaneWidth()))
	}
	var lines int
	switch kind {
	case focusScrollUp:
		lines = -focusScrollStep
	case focusScrollDown:
		lines = focusScrollStep
	case focusScrollPageUp:
		lines = -max(1, rows-1)
	case focusScrollPageDown:
		lines = max(1, rows-1)
	case focusScrollTop:
		lines = -whole
	case focusScrollBottom:
		lines = whole
	}
	if m.showsConversation() {
		return m.scrollConversation(lines)
	}
	if m.pane.mouse {
		box := m.pane.box
		if !box.ok {
			return nil
		}
		notches := max(1, abs(lines)/focusScrollStep)
		return m.forwardWheel(lines < 0, box.height/2, box.width/2, notches)
	}
	return m.scrollFocusLines(lines)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// wheelPreview routes one wheel notch in the list. Only the preview takes
// it, and only while the pointer is over the pane it paints: the pointer is
// the whole selector, which is what a tablet reports too -- a scroll gesture
// there arrives at the cell it was made over, there being no pointer to
// hover with first. A notch anywhere else in the list is swallowed.
func (m *Model) wheelPreview(up bool, x, y int) tea.Cmd {
	if _, _, inside := m.paneCell(x, y); !inside {
		return nil
	}
	return m.wheelFocus(up, x, y)
}

// forwardFocusMouse sends an explicit Alt-modified click lifecycle to a
// focused application that owns the mouse. Normal clicks remain available
// for Gate Inbox's selection and clipboard behavior.
func (m *Model) forwardFocusMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	row, col, ok := m.forwardedMouseCell(msg)
	if !ok {
		return m, nil
	}
	button := m.forwardedMouseButton(msg)
	_, release := msg.(tea.MouseReleaseMsg)
	if _, motion := msg.(tea.MouseMotionMsg); motion {
		button |= motionBit
	}
	report, ok := m.mouseReport(button, release, col, row+m.paneRowOffset(m.pane.box.height))
	if !ok {
		return m, nil
	}
	m.sendFocusReport(report)
	return m, nil
}

// sendFocusReport delivers encoded mouse bytes to the focused session's
// pane. Every mouse report the manager forwards funnels through here, which
// is also what tells the poller the repaint that follows is the operator's
// doing. SendRawAt takes the pooled pipe where the server has one.
func (m *Model) sendFocusReport(report string) {
	sess, ok := m.selected()
	if !ok {
		return
	}
	m.poller.noteOperatorInput(sess.ID)
	command := "send-keys -t " + m.tmux.TargetName(sess.ID) + " -H " + hexBytes(report)
	if err := m.tmux.SendRawAt(sess.ID, command); err != nil {
		m.errBar.text = err.Error()
	}
}

// forwardedMouseCell uses the current cell while the pointer is in the pane.
// A release immediately after leaving the pane belongs to the active gesture,
// so it lands at that gesture's final in-pane cell instead.
func (m *Model) forwardedMouseCell(msg tea.MouseMsg) (row, col int, ok bool) {
	mouse := msg.Mouse()
	if row, col, inside := m.paneCell(mouse.X, mouse.Y); inside {
		m.forwardingRow, m.forwardingCol = row, col
		return row, col, true
	}
	if _, release := msg.(tea.MouseReleaseMsg); release && m.forwardingMouse {
		return m.forwardingRow, m.forwardingCol, true
	}
	return 0, 0, false
}

// forwardedMouseButton keeps an X10 release paired with the press that
// started its Alt-forwarded lifecycle. Bubble Tea reports X10 releases as
// MouseNone, while SGR needs the button that was released.
func (m *Model) forwardedMouseButton(msg tea.MouseMsg) int {
	mouse := msg.Mouse()
	if _, release := msg.(tea.MouseReleaseMsg); release && mouse.Button == tea.MouseNone {
		return m.forwardingButton
	}
	return mouseButton(mouse.Button)
}

func mouseButton(button tea.MouseButton) int {
	switch button {
	case tea.MouseMiddle:
		return middleButton
	case tea.MouseRight:
		return rightButton
	default:
		return leftButton
	}
}

// scrollFocus moves the focused pane through its scrollback and puts the
// region that lands on screen in front of the operator. Negative delta
// scrolls up into history. Scrolling stops the live view the same way
// tmux's own copy mode does: live-bottom frames are ignored until the pane
// is back at the bottom.
func (m *Model) scrollFocus(delta int) tea.Cmd {
	return m.scrollFocusLines(delta * focusScrollStep)
}

// scrollFocusLines is scrollFocus in lines rather than notches: negative
// moves up into history, positive back toward the live bottom.
func (m *Model) scrollFocusLines(lines int) tea.Cmd {
	sess, ok := m.selected()
	if !ok || (m.mode != modeFocus && m.mode != modeList) {
		return nil
	}
	offset := clampFocusOffset(m.focusScroll-lines, m.pane.history)
	if offset == m.focusScroll {
		return nil
	}
	m.focusScroll = offset
	return m.requestFocusRegion(sess.ID)
}

// clampFocusOffset keeps a scroll offset inside the pane's history.
func clampFocusOffset(offset, history int) int {
	return min(max(offset, 0), max(history, 0))
}

// reclampFocusScroll pulls a scrolled-back offset inside whatever history
// the pane has now. A resize reflows the pane and a pane's history shrinks
// when its app clears it, and either can leave the offset past the end,
// where the capture reads nothing.
func (m *Model) reclampFocusScroll() {
	if m.focusScroll != 0 {
		m.focusScroll = clampFocusOffset(m.focusScroll, m.pane.history)
	}
}

// requestFocusRegion reads whatever the focused pane is currently scrolled
// to: a wheel notch that just moved it, a resize that has to repaint the
// frame, or a keystroke pulling it back to the live bottom.
//
// There used to be a cache of scrollback blocks here, read ahead in the
// direction of travel, and a wheel notch was served from it where it could
// be. It went when the mirror did: the only thing that ever invalidated it
// was the mirror announcing that the pane had painted, so a remembered frame
// could no longer be kept honest.
//
// What replaced it is not a cheaper read. A region capture is a forked tmux
// process at around three milliseconds -- captures do not take the pooled
// pipe, whatever the paragraph here said while they briefly did; see the note
// above CaptureScrollback. What keeps that affordable is the bound below:
// one read out at a time, so a flick costs two captures rather than one per
// notch. Read the cost before tuning the cadence around it.
//
// One read is out at a time. A capture is a fork -- CapturePane and
// CaptureRegion both are, deliberately -- and a flick is dozens of notches,
// so a read per notch put two hundred captures in flight inside one second on
// the operator's board. They starved each other: each took 1.8 seconds
// instead of five milliseconds, every frame that landed was for an offset the
// wheel had long passed, and each of those asked for another read. A burst
// now costs two reads: this one, and the one its reply issues for wherever
// the wheel stopped.
func (m *Model) requestFocusRegion(sessID string) tea.Cmd {
	// A read whose reply never came back must not wedge the wheel for good.
	// Nothing in the event loop drops a command, but a capture can hang on a
	// server that has stopped answering, and a dead wheel is a worse failure
	// than one extra capture a second.
	if m.focusReading && time.Since(m.focusReadingAt) < focusReadStale {
		return nil
	}
	m.focusReading, m.focusReadingAt = true, time.Now()
	return m.focusRegionCmd(sessID, m.focusScroll)
}

// focusRegionCmd captures a block of pane around the region that sits offset
// lines above the live bottom. tmux numbers the visible screen from 0 down,
// and history above it with negative lines, so a scrolled window is just a
// shifted start and end.
//
// It reads exactly the frame it was asked for. CaptureRegion forks, on every
// server, so an adopted pane is read the same way as one the manager started
// -- and at the same price, which is why requestFocusRegion lets only one of
// these be out at a time.
func (m *Model) focusRegionCmd(sessID string, offset int) tea.Cmd {
	rows, width := m.previewPaneHeight(), m.previewPaneWidth()
	start, end := -offset, rows-1-offset
	driver := m.tmux
	return func() tea.Msg {
		msg := focusScrollMsg{sessID: sessID, offset: offset, rows: rows, width: width}
		out, err := driver.CaptureRegion(sessID, start, end)
		msg.block, msg.ok = out, err == nil
		return msg
	}
}

// applyFocusScroll lands a region capture. A capture whose target moved while
// it was in flight -- the wheel turned again, or the panel was resized -- is
// a frame for a pane the operator is no longer on, so it is re-read rather
// than painted.
func (m *Model) applyFocusScroll(sessID string, msg focusScrollMsg) tea.Cmd {
	m.focusReading = false
	rows, width := m.previewPaneHeight(), m.previewPaneWidth()
	if msg.offset != m.focusScroll || msg.rows != rows || msg.width != width {
		// The target moved while this read was out -- more notches, or a
		// resize. One read for where it is now, not one per notch that moved
		// it.
		return m.requestFocusRegion(sessID)
	}
	if msg.ok {
		m.preview = msg.block
	}
	return nil
}

// scrolledBack reports whether the pane on screen -- the focused one, or
// the list's preview of the selected session -- is showing history rather
// than its live bottom.
func (m *Model) scrolledBack() bool {
	return m.focusScroll > 0
}
