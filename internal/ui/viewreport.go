package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/keymap"
)

// viewReport is the last place the board told its extensions the operator
// was in, so it reports changes rather than every update.
type viewReport struct {
	sent      bool
	visible   bool
	place     extension.ViewPlace
	sessionID string
	triage    bool
	filtered  bool
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
	now := viewReport{sent: true, visible: m.visible, place: extension.ViewList,
		triage: m.triage, filtered: m.searching || strings.TrimSpace(m.search) != ""}
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
		Triage:    now.triage,
		Filtered:  now.filtered,
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
		Triage:    m.lastView.triage,
		Filtered:  m.lastView.filtered,
		At:        time.Now(),
	})
}

// reportAction tells board extensions the operator ran action, or sent the
// snippet on menu key snippet. It is called before the action runs, so the
// event says where the operator was when they asked for it rather than where
// it took them.
func (m *Model) reportAction(action, snippet string, via extension.ViewVia) {
	observer := m.viewObserver()
	if observer == nil {
		return
	}
	m.reportView()
	sessionID := m.lastView.sessionID
	if m.lastView.place == extension.ViewList {
		sessionID = m.cursorSessionID()
	}
	observer.View(extension.ViewEvent{
		Kind:      extension.ViewAction,
		Visible:   m.lastView.visible,
		Place:     m.lastView.place,
		SessionID: sessionID,
		Triage:    m.lastView.triage,
		Filtered:  m.lastView.filtered,
		Action:    action,
		Snippet:   snippet,
		Via:       via,
		At:        time.Now(),
	})
}

// cursorActions are reported as ViewCursor, with the rows they moved.
var cursorActions = map[keymap.Action]bool{
	keymap.CursorUp: true, keymap.CursorDown: true, keymap.CursorTop: true, keymap.CursorBottom: true,
}

// reportKey reports the action a key press is about to run, read the way the
// screen it lands on will read it.
func (m *Model) reportKey(msg tea.KeyPressMsg) {
	if m.viewObserver() == nil {
		return
	}
	if action, snippet, via, ok := m.pressedAction(msg); ok && !cursorActions[action] {
		m.reportAction(string(action), snippet, via)
	}
}

// pressedAction is the action msg is bound to on the screen that will take
// it, or the snippet it sends. ok is false for a key that reaches a session,
// is typed into a field or a search, or lands on a screen that is not the
// list, a focused session or one of the dialogs the key map names.
//
// It follows handleKey's order for the screens it reports: the hotkey menu
// answers its own few actions and then its snippets, a screen's bindings come
// before a snippet's chord, and the one snippet key outside the menu last.
func (m *Model) pressedAction(msg tea.KeyPressMsg) (keymap.Action, string, extension.ViewVia, bool) {
	ctx, ok := m.pressContext()
	if !ok || (ctx == keymap.ContextList && !m.quick.active && m.isGroupJumpKey(keyName(msg))) {
		return "", "", "", false
	}
	action, bound := m.sideAction(ctx, msg)
	if m.quick.active {
		if bound && (action == keymap.QuickInput || action == keymap.ToggleConversation || action == keymap.Rescind) {
			return action, "", extension.ViaKey, true
		}
		if snip, found := m.menuSnippetFor(msg.String()); found {
			return extension.ActionSnippet, snip.Key, extension.ViaMenu, true
		}
		return "", "", "", false
	}
	if bound {
		return action, "", extension.ViaKey, true
	}
	if ctx != keymap.ContextList && ctx != keymap.ContextFocus {
		return "", "", "", false
	}
	if snip, found := m.snippetChordFor(msg); found {
		return extension.ActionSnippet, snip.Key, extension.ViaKey, true
	}
	if snip, found := m.snippetFor(msg.String()); found {
		return extension.ActionSnippet, snip.Key, extension.ViaKey, true
	}
	return "", "", "", false
}

// pressContext is the key map context a press is read against on the screen
// that will take it, or false for a screen whose keys are typed or are its
// own.
func (m *Model) pressContext() (keymap.Context, bool) {
	if m.split.resizeMode {
		return "", false
	}
	switch m.mode {
	case modeFocus:
		return keymap.ContextFocus, true
	case modeConfirmDelete:
		return keymap.ContextConfirm, true
	case modeNameSweep:
		return keymap.ContextNameSweep, true
	case modeWelcome:
		return keymap.ContextWelcome, true
	case modeLimitSwitch:
		return keymap.ContextLimitSwitch, true
	case modeList:
	default:
		return "", false
	}
	switch {
	case m.searching, m.legendPeek.visible:
		return "", false
	case m.reviewPageOpen():
		return keymap.ContextReviewPage, true
	case m.onReviewsTab():
		return keymap.ContextReviews, true
	}
	return keymap.ContextList, true
}

func (m *Model) viewObserver() BoardObserver {
	if m.poller == nil {
		return nil
	}
	return m.poller.observer
}
