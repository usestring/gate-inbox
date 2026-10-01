package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/search"
)

// A focused session shows its conversation by default: entering a session to
// read what it said keeps the turns in front of you, and F3 hands the pane
// back to the agent when it is time to type. The test fixtures pin the
// terminal (see helpers_test), so this reads the default the way a fresh run
// resolves it and then focuses a model carrying it.
func TestFocusedViewDefaultsToTheConversation(t *testing.T) {
	m := drainFleet(t)
	if got := storedFocusView(m.store); got != focusViewConversation {
		t.Fatalf("storedFocusView = %q, want the conversation default", got)
	}
	m.focusView = storedFocusView(m.store)
	m = enterDrain(t, m)
	if m.focusView != focusViewConversation {
		t.Fatalf("focusView = %q, want the conversation", m.focusView)
	}
	if !m.showsConversation() {
		t.Fatal("a session opened on the terminal, not the conversation")
	}
}

// With the setting on, a focused session keeps its conversation on screen --
// the same shortened transcript the list shows -- and F3 switches to the
// terminal for this visit. The choice is persisted, so it is not a fluke.
func TestFocusedViewKeepsTheConversationUntilToggled(t *testing.T) {
	m := drainFleet(t)
	m.focusView = focusViewConversation
	m = enterDrain(t, m)
	if !m.showsConversation() {
		t.Fatal("the focused-view setting did not keep the conversation on screen")
	}
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{
		key:      conversationKey(sess.ID, sess.AgentSessionID),
		messages: []search.Message{{Role: "assistant", Text: "hello from focus"}},
	})
	if got := ansi.Strip(strings.Join(m.conversationBody(80), "\n")); !strings.Contains(got, "hello from focus") {
		t.Fatalf("focused conversation did not render: %q", got)
	}

	cmd := m.toggleConversation()
	if m.focusView != focusViewTerminal {
		t.Fatalf("F3 left focusView on %q, want the terminal", m.focusView)
	}
	if m.showsConversation() {
		t.Fatal("the terminal view still shows the conversation")
	}
	if cmd == nil {
		t.Fatal("F3 did not persist the choice")
	}
	m.applyCmd(t, cmd)
	if got, _ := m.store.Setting(focusViewSetting); got != focusViewTerminal {
		t.Fatalf("stored focus view = %q, want %q", got, focusViewTerminal)
	}

	// F3 again brings the conversation back and persists that too.
	m.applyCmd(t, m.toggleConversation())
	if !m.showsConversation() {
		t.Fatal("F3 did not return to the conversation")
	}
	if got, _ := m.store.Setting(focusViewSetting); got != focusViewConversation {
		t.Fatalf("stored focus view = %q, want %q", got, focusViewConversation)
	}
}

// A shell has no conversation to show: the toggle from inside one does
// nothing rather than switching the focused view to an empty transcript.
func TestFocusedConversationToggleRefusesAShell(t *testing.T) {
	m := drainFleet(t)
	m.focusView = focusViewConversation
	m = enterDrain(t, m)
	m.rows[m.cursor].sess.Tool = "terminal"

	if cmd := m.toggleConversation(); cmd != nil {
		t.Fatal("a shell took the focused view toggle")
	}
	if m.focusView != focusViewConversation {
		t.Fatalf("a shell moved the focused view to %q", m.focusView)
	}
}

// While the next row's conversation is being read, the last one on screen
// stands in rather than flashing the empty placeholder; once the read is done
// and that row has no transcript, the placeholder is the honest answer.
func TestConversationKeepsTheLastRowsWhileTheNextLoads(t *testing.T) {
	m := drainFleet(t)
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{
		key:      conversationKey(sess.ID, sess.AgentSessionID),
		messages: []search.Message{{Role: "assistant", Text: "old reply"}},
	})

	// The cursor has moved: the stored conversation belongs to the row just
	// left, and a read for the new one is out.
	m.conversation.key = conversationKey("moved-on", "")
	m.conversation.busy, m.conversation.pendingKey = true, conversationKey("moved-on", "")
	got := ansi.Strip(strings.Join(m.conversationRows(80, 20), "\n"))
	if strings.Contains(got, "No user-facing messages yet") || !strings.Contains(got, "old reply") {
		t.Fatalf("the last conversation did not stand in while the next loaded: %q", got)
	}

	m.conversation.busy, m.conversation.pendingKey = false, ""
	got = ansi.Strip(strings.Join(m.conversationRows(80, 20), "\n"))
	if !strings.Contains(got, "No user-facing messages yet") {
		t.Fatalf("with the read done the placeholder should stand: %q", got)
	}
}
