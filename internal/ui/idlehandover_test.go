package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/notify"
	"github.com/usestring/gate-inbox/internal/status"
)

// The idle switch moves an operator who is not at the keys, and the focus
// view looks the same after it, so the board says so: one notification
// naming where they went and why. Without it the switch was reported as
// the board jumping back to the first pane at random.
func TestIdleHandoverNotifiesTheOperator(t *testing.T) {
	m := buildModel(t)
	busy := liveHookedSession(t, m, "busy")
	asking := liveHookedSession(t, m, "asking")
	writeHookStatus(t, m, busy.ID, status.Working)
	writeHookStatus(t, m, asking.ID, status.Waiting)
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "busy", status.Working)
	requireStatus(t, m, "asking", status.Waiting)

	var notes []notify.Note
	m.poller.escalate = func(note notify.Note) bool { notes = append(notes, note); return true }
	m.enterFocusOn(t, "busy")

	now := time.Now()
	m.lastInteractionAt = now.Add(-time.Minute)
	if cmd := m.handOverOnIdle(now); cmd != nil || len(notes) != 0 {
		t.Fatalf("a minute without keys switched or notified: %d notes", len(notes))
	}

	m.lastInteractionAt = now.Add(-4 * time.Minute)
	m.applyCmd(t, m.handOverOnIdle(now))
	if m.mode != modeFocus || focusedName(t, m) != "asking" {
		t.Fatalf("idle switch left mode %v on %q, want focus on asking", m.mode, focusedName(t, m))
	}
	if len(notes) != 1 {
		t.Fatalf("idle switch sent %d notifications, want 1", len(notes))
	}
	note := notes[0]
	if note.Subject != "asking" || !strings.Contains(note.Body, "busy") || !strings.Contains(note.Body, "4 min") {
		t.Fatalf("notification %+v does not name the destination, the session left and the idle time", note)
	}
}
