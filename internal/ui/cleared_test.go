package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
)

func drainedBoard(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	adoptForeignPane(t, m, "busy", "busy", status.Working)
	loadStoredRows(t, m)
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
	m.selectSessionRow(t, "busy")
	frame := ansi.Strip(m.frame())
	if strings.Contains(frame, selectionBorder.Left) {
		t.Fatalf("the rail shows a selected row while the prompt has focus:\n%s", frame)
	}
	if strings.Contains(ansi.Strip(m.viewFooter()), "attach") {
		t.Fatal("the footer offers actions on a session while the prompt has focus")
	}
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

func TestBackFromInboxZeroReturnsToTheLastFocusedPane(t *testing.T) {
	for _, chord := range []string{"ctrl+l", "alt+;"} {
		t.Run(chord, func(t *testing.T) {
			m := buildModel(t)
			adoptForeignPane(t, m, "earlier", "earlier", status.Working)
			adoptForeignPane(t, m, "last", "last", status.Working)
			loadStoredRows(t, m)
			focusRow(t, m, "earlier")
			m.leaveFocusForFixture(t)
			focusRow(t, m, "last")
			m.leaveFocusForFixture(t)
			setStatus(t, m, "earlier", status.Working)
			setStatus(t, m, "last", status.Working)
			loadStoredRows(t, m)
			m.selectSessionRow(t, "last")
			last, _ := m.selected()
			m.mute(last)
			m.applyCmd(t, m.toggleTriage())
			if m.mode != modeCleared {
				t.Fatalf("draining left mode %v, want inbox zero", m.mode)
			}

			_, cmd := m.Update(key(chord))
			m.applyCmd(t, cmd)
			if got := lastPaneName(t, m); got != "last" {
				t.Fatalf("back focused %q, want last", got)
			}
			if m.isMuted(last) {
				t.Fatal("back left the reopened session muted")
			}

			_, cmd = m.Update(key(chord))
			m.applyCmd(t, cmd)
			if got := lastPaneName(t, m); got != "earlier" {
				t.Fatalf("second back focused %q, want earlier", got)
			}
		})
	}
}

func TestPrintableBackBindingDoesNotInterruptInboxZeroDraft(t *testing.T) {
	m := drainedBoard(t)
	m.noteFocused("busy")
	keys, problems := keymap.New(keymap.Overrides{
		keymap.ContextList: {keymap.LastPane: {"l"}},
	})
	if len(problems) > 0 {
		t.Fatalf("could not remap list Back: %v", problems)
	}
	m.keys = keys
	m.typeInto(t, "he")

	_, cmd := m.Update(key("l"))
	m.applyCmd(t, cmd)
	if got := m.form.prompt.input.Value(); m.mode != modeCleared || got != "hel" {
		t.Fatalf("typing l left mode %v and draft %q, want Inbox Zero and hel", m.mode, got)
	}

	_, cmd = m.Update(key("alt+;"))
	m.applyCmd(t, cmd)
	if got := lastPaneName(t, m); got != "busy" {
		t.Fatalf("Back chord focused %q, want busy", got)
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
