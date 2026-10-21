package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// drainOnCapture is drainOnDialog with the focused pane showing a captured
// dialog from the dialog package's testdata.
func drainOnCapture(t *testing.T, capture string) *Model {
	t.Helper()
	pane, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", capture))
	if err != nil {
		t.Fatal(err)
	}
	m := drainOnDialog(t, true)
	m.preview = string(pane)
	return m
}

// dialogCaptures are held dialogs, each with whether a key answering it ends
// what the session asked. The ones that do not are a step of a dialog with
// further to go: Enter ticks a box or answers one question of several.
var dialogCaptures = []struct {
	capture string
	ends    bool
}{
	{"claude-2.1.284-w50-stepper4-first.ansi", false},
	{"claude-2.1.283-tabs-w80-second.ansi", false},
	{"claude-2.1.284-w50-stepper4-multiselect-ticked.ansi", false},
	{"claude-2.1.283-tabs-w44-multiselect.ansi", false},
	{"claude-2.1.286-w50-multiselect.ansi", false},
	{"claude-2.1.286-w40-multiselect.ansi", false},
	{"claude-2.1.283-tabs-w80-revisit-answered.ansi", false},
	// A stripped tab row cannot say which tab is up.
	{"claude-2.1.283-tabs-reported-narrow.txt", false},
	{"claude-2.1.283-tabs-w80-review-complete.ansi", true},
	{"claude-2.1.283-tabs-w80-submit-unanswered.ansi", true},
	{"claude-2.1.284-w50-tabs-review-complete.ansi", true},
	{"claude-2.1.284-w50-approval-asked.txt", true},
	{"claude-2.1.284-w50-permission-touch.txt", true},
	{"opencode/opencode-2.0.3-multi1-w100.txt", false},
	{"opencode/opencode-2.0.3-multi1-ticked-w40.txt", false},
	{"opencode/opencode-2.0.3-q3-t1-w100.txt", false},
	{"opencode/opencode-2.0.3-q3-t2-w100.txt", false},
	{"opencode/opencode-2.0.3-q3-t2-custom-typed-w100.txt", false},
	{"opencode/opencode-2.0.3-q2-t2-after-first-w100.txt", false},
	{"opencode/opencode-2.0.3-q1-w100.txt", true},
	{"opencode/opencode-2.0.3-multi1-review-w100.txt", true},
	{"opencode/opencode-2.0.3-q3-review-w100.txt", true},
	{"codex/codex-0.157.0-ask2-q1-w100.txt", false},
	{"codex/codex-0.157.0-ask2-q2-w100.txt", true},
	{"codex/codex-0.157.0-ask1-w100.txt", true},
	{"codex/codex-0.157.0-exec-approval-w100.txt", true},
}

func TestKeyEndsDialog(t *testing.T) {
	for _, tc := range dialogCaptures {
		t.Run(tc.capture, func(t *testing.T) {
			m := drainOnCapture(t, tc.capture)
			if got := m.keyEndsDialog(); got != tc.ends {
				t.Fatalf("keyEndsDialog = %v, want %v", got, tc.ends)
			}
		})
	}
}

// Enter on a step of a dialog with further to go keeps the operator on it,
// and its landing does not move the drain either: the session still has the
// rest of the dialog to ask. Enter that ends the dialog hands over at once.
func TestEnterHandsOverOnlyWhenItEndsTheDialog(t *testing.T) {
	for _, tc := range dialogCaptures {
		t.Run(tc.capture, func(t *testing.T) {
			m := drainOnCapture(t, tc.capture)
			askID := focusedID(t, m)
			m = pressEnter(m)
			want := "ask"
			if tc.ends {
				want = "next"
			}
			if got := focusedName(t, m); got != want {
				t.Fatalf("Enter focused %q, want %q", got, want)
			}
			if tc.ends {
				return
			}
			if m.handoverTrace.to != "" {
				t.Fatal("an Enter that stayed armed a handover trace")
			}
			landAnswer(t, m, askID)
			if got := focusedName(t, m); got != "ask" {
				t.Fatalf("a step of the dialog landing moved the drain to %q", got)
			}
		})
	}
}

// A digit ticks a multi-select box just as Enter does, so its landing keeps
// the operator on the dialog.
func TestADigitOnAMultiSelectDoesNotHandOver(t *testing.T) {
	for _, capture := range []string{
		"claude-2.1.286-w50-multiselect.ansi",
		"opencode/opencode-2.0.3-multi1-w100.txt",
	} {
		t.Run(capture, func(t *testing.T) {
			m := drainOnCapture(t, capture)
			askID := focusedID(t, m)
			updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: '1', Text: "1"})
			m = updated.(*Model)
			if pending, ok := m.landings[askID]; ok {
				if pending.handOver {
					t.Fatal("a tick on a multi-select armed a handover")
				}
				landAnswer(t, m, askID)
			}
			if got := focusedName(t, m); got != "ask" {
				t.Fatalf("a tick on a multi-select moved the drain to %q", got)
			}
		})
	}
}
