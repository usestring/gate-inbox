package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func altDot() tea.KeyPressMsg { return tea.KeyPressMsg{Code: '.', Text: ".", Mod: tea.ModAlt} }

func pressFocused(t *testing.T, m *Model, key tea.KeyPressMsg) *Model {
	t.Helper()
	updated, _ := m.handleFocusKey(key)
	return updated.(*Model)
}

// drainFleet is two sessions waiting, so there is a head to enter and a
// second one for the drain to promote.
func drainFleet(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.rebuildRows()
	return m
}

// enterDrain is triage on with the head of its queue open.
func enterDrain(t *testing.T, m *Model) *Model {
	t.Helper()
	if !m.triage {
		m.applyCmd(t, m.toggleTriage())
	}
	m.enterFocusOn(t, "ask")
	return m
}

func sessionByID(t *testing.T, m *Model, id string) store.Session {
	t.Helper()
	for _, sess := range m.sessions {
		if sess.ID == id {
			return sess
		}
	}
	t.Fatalf("no session %q on the board", id)
	return store.Session{}
}

func TestFocusedDismissSkipsToTheNext(t *testing.T) {
	m := enterDrain(t, drainFleet(t))

	m = pressFocused(t, m, altDot())

	if m.mode != modeFocus {
		t.Fatalf("the skip dropped out of the drain: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("the skip landed on %q, want the next in the queue", got)
	}
}

func TestFocusBackReopensTheSessionItLeft(t *testing.T) {
	m := enterDrain(t, drainFleet(t))
	first := focusedID(t, m)

	m = pressFocused(t, m, altDot())
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("skip landed on %q, want next", got)
	}
	left := sessionByID(t, m, first)
	if !m.isMuted(left) {
		t.Fatal("the skip did not mute the session it left")
	}

	m = pressFocused(t, m, jumpKey(t, "alt+l"))

	if got := focusedID(t, m); got != first {
		t.Fatalf("back landed on %q, want the session it left", got)
	}
	if m.isMuted(left) {
		t.Fatal("back left the session muted")
	}
}

func TestFocusedCopySessionIDWritesTheConversationID(t *testing.T) {
	m := enterDrain(t, drainFleet(t))
	sess, _ := m.selected()
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].AgentSessionID = "conv-abc-123"
		}
	}
	m.rebuildRows()

	orig := writeClipboard
	defer func() { writeClipboard = orig }()
	var copied string
	writeClipboard = func(id string) error {
		copied = id
		return nil
	}

	updated, cmd := m.handleFocusKey(jumpKey(t, "alt+y"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}

	if copied != "conv-abc-123" {
		t.Fatalf("clipboard got %q, want the conversation id", copied)
	}
	if !strings.Contains(m.errBar.text, "copied session id") {
		t.Fatalf("the status line said %q", m.errBar.text)
	}
}
