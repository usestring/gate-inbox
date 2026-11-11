package ui

import (
	"time"

	"github.com/usestring/gate-inbox/extension"
)

// viewReport is the last place the board told its extensions the operator
// was in, so it reports changes rather than every update.
type viewReport struct {
	sent      bool
	visible   bool
	place     extension.ViewPlace
	sessionID string
}

// reportView tells board extensions where the operator is when that has
// changed since the last report. It runs after every update, so whichever of
// the many paths into or out of a focused pane was taken, the change is
// reported once.
func (m *Model) reportView() {
	observer := m.viewObserver()
	if observer == nil {
		return
	}
	now := viewReport{sent: true, visible: m.visible, place: extension.ViewList}
	switch {
	case m.attachedID != "":
		now.place, now.sessionID = extension.ViewAttach, m.attachedID
	case m.mode == modeFocus:
		now.place, now.sessionID = extension.ViewFocus, m.cursorSessionID()
	}
	if now == m.lastView {
		return
	}
	m.lastView = now
	observer.View(extension.ViewEvent{
		Kind:      extension.ViewChanged,
		Visible:   now.visible,
		Place:     now.place,
		SessionID: now.sessionID,
		At:        time.Now(),
	})
}

// reportCursor tells board extensions the operator moved the cursor from row
// from with a key.
func (m *Model) reportCursor(from int) {
	observer := m.viewObserver()
	if observer == nil || m.cursor == from {
		return
	}
	// A move can come before the update's own report: say where the
	// operator is first, so a subscriber never sees a move from nowhere.
	m.reportView()
	observer.View(extension.ViewEvent{
		Kind:      extension.ViewCursor,
		Visible:   m.visible,
		Place:     extension.ViewList,
		SessionID: m.cursorSessionID(),
		Rows:      m.cursor - from,
		At:        time.Now(),
	})
}

func (m *Model) viewObserver() BoardObserver {
	if m.poller == nil {
		return nil
	}
	return m.poller.observer
}
