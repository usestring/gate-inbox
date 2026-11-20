package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/status"
)

func drainedBoard(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"busy": status.Working})
	m.applyCmd(t, m.toggleTriage())
	if m.mode != modeCleared {
		t.Fatalf("a drained queue left mode %v, want the inbox-cleared pane", m.mode)
	}
	return m
}

// The pane replaces the session preview beside the list rather than taking
// the screen the way the new-session form does.
func TestTheClearedPaneDrawsBesideTheList(t *testing.T) {
	m := drainedBoard(t)
	frame := ansi.Strip(m.frame())
	if !strings.Contains(frame, "inbox zero") || !strings.Contains(frame, `\o/`) {
		t.Fatalf("the frame does not show the congratulations:\n%s", frame)
	}
	if !strings.Contains(frame, "busy") {
		t.Fatalf("the list is not on screen beside the pane:\n%s", frame)
	}
	for _, want := range []string{"launch", "dismiss"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the pane does not offer %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "children") || strings.Contains(frame, "perms") {
		t.Fatalf("the pane draws the full form's fields:\n%s", frame)
	}
}

func TestEscDismissesTheClearedPane(t *testing.T) {
	m := drainedBoard(t)
	m.typeInto(t, "draft")
	m.handleClearedKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mode != modeList {
		t.Fatalf("esc left mode %v, want the list", m.mode)
	}
	if !m.triage {
		t.Fatal("dismissing the pane left triage")
	}
}

// The list is on screen, so its triage toggle works without a dismiss first.
func TestTheTriageKeyLeavesTriageFromTheClearedPane(t *testing.T) {
	m := drainedBoard(t)
	updated, cmd := m.handleKey(key("ctrl+i"))
	m = updated.(*Model)
	m.applyCmd(t, cmd)
	if m.triage || m.mode != modeList {
		t.Fatalf("the triage key left triage %t mode %v, want the tree", m.triage, m.mode)
	}
}

// An untouched pane is no draft to protect, so work that comes back takes the
// operator straight to it; a typed prompt holds the pane (see
// TestTriagePicksUpFinishedArrivingAfterDrain).
func TestAnUntouchedClearedPaneYieldsToReturningWork(t *testing.T) {
	m := drainedBoard(t)
	if !m.clearedYields() {
		t.Fatal("an empty pane refuses the pickup")
	}
	m.typeInto(t, "x")
	if m.clearedYields() {
		t.Fatal("a typed prompt yields the pane")
	}
}

// Auto leaves the launch to the operator's routing; stepping the picker pins
// it to a listed subscription, and enter launches on that one.
func TestTheClearedPanePinsAChosenSubscription(t *testing.T) {
	m := drainedBoard(t)
	withAccountTools(m)
	m.selectTool("claude")
	m.cleared = clearedState{}
	if got := m.clearedAccount(); got != "" {
		t.Fatalf("a fresh pane pins %q, want auto", got)
	}
	m.handleClearedKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if len(m.cleared.accounts) < 2 || m.cleared.accounts[0] != accountAuto {
		t.Fatalf("listed subscriptions %v, want auto then the accounts", m.cleared.accounts)
	}
	want := m.cleared.accounts[1]
	if got := m.clearedAccount(); got != want {
		t.Fatalf("one step down pins %q, want %q", got, want)
	}
	m.handleClearedKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.form.account != want {
		t.Fatalf("enter launched on %q, want %q", m.form.account, want)
	}
	m.handleClearedKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.selectedToolName() != "claude" && m.clearedAccount() != "" {
		t.Fatal("changing the CLI kept the last CLI's subscription")
	}
}
