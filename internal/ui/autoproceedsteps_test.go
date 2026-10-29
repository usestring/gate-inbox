package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
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

var dialogCaptures = []string{
	"claude-2.1.284-w50-stepper4-first.ansi",
	"claude-2.1.283-tabs-w80-second.ansi",
	"claude-2.1.284-w50-stepper4-multiselect-ticked.ansi",
	"claude-2.1.283-tabs-w44-multiselect.ansi",
	"claude-2.1.286-w50-multiselect.ansi",
	"claude-2.1.286-w40-multiselect.ansi",
	"claude-2.1.283-tabs-w80-revisit-answered.ansi",
	"claude-2.1.283-tabs-reported-narrow.txt",
	"claude-2.1.283-tabs-w80-review-complete.ansi",
	"claude-2.1.283-tabs-w80-submit-unanswered.ansi",
	"claude-2.1.284-w50-tabs-review-complete.ansi",
	"claude-2.1.284-w50-approval-asked.txt",
	"claude-2.1.284-w50-permission-touch.txt",
	"opencode/opencode-2.0.3-multi1-w100.txt",
	"opencode/opencode-2.0.3-multi1-ticked-w40.txt",
	"opencode/opencode-2.0.3-q3-t1-w100.txt",
	"opencode/opencode-2.0.3-q3-t2-w100.txt",
	"opencode/opencode-2.0.3-q3-t2-custom-typed-w100.txt",
	"opencode/opencode-2.0.3-q2-t2-after-first-w100.txt",
	"opencode/opencode-2.0.3-q1-w100.txt",
	"opencode/opencode-2.0.3-multi1-review-w100.txt",
	"opencode/opencode-2.0.3-q3-review-w100.txt",
	"codex/codex-0.157.0-ask2-q1-w100.txt",
	"codex/codex-0.157.0-ask2-q2-w100.txt",
	"codex/codex-0.157.0-ask1-w100.txt",
	"codex/codex-0.157.0-exec-approval-w100.txt",
}

func TestDialogEnterWaitsForObservedCompletion(t *testing.T) {
	for _, capture := range dialogCaptures {
		t.Run(capture, func(t *testing.T) {
			m := drainOnCapture(t, capture)
			askID := focusedID(t, m)
			m = pressEnter(m)
			if got := focusedName(t, m); got != "ask" {
				t.Fatalf("Enter focused %q before the dialog completed", got)
			}
			pending := onlyLanding(t, m)
			if !pending.probe.dialog || !pending.handOver || pending.early {
				t.Fatal("the dialog answer is not waiting for completion")
			}
			m.applyLandingCheck(landingCheckMsg{id: askID, gen: pending.gen, verdict: landingPending})
			if got := focusedName(t, m); got != "ask" {
				t.Fatalf("an open dialog moved focus to %q", got)
			}
			landAnswer(t, m, askID)
			if got := focusedName(t, m); got != "next" {
				t.Fatalf("a completed dialog left focus on %q", got)
			}
		})
	}
}

func TestADigitOnAnOpenMultiSelectDoesNotHandOver(t *testing.T) {
	for _, capture := range []string{
		"claude-2.1.286-w50-multiselect.ansi",
		"opencode/opencode-2.0.3-multi1-w100.txt",
	} {
		t.Run(capture, func(t *testing.T) {
			m := drainOnCapture(t, capture)
			updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: '1', Text: "1"})
			m = updated.(*Model)
			if len(m.landings) > 0 {
				lookForLanding(t, m)
			}
			if got := focusedName(t, m); got != "ask" {
				t.Fatalf("a tick on a multi-select moved the drain to %q", got)
			}
		})
	}
}

func TestOpencodeCustomAnswerKeepsFocusUntilWorkResumes(t *testing.T) {
	m := drainOnCapture(t, "opencode/opencode-2.0.3-q1-w100.txt")
	for i := range m.sessions {
		m.sessions[i].Tool = "opencode"
		m.sessions[i].Status = status.Idle
	}
	m.rebuildRows()
	engine, err := status.NewEngine(config.Config{Tools: map[string]config.Tool{
		"opencode": {Rules: []config.Rule{{State: status.Working, Pattern: "."}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	current := strings.ReplaceAll(m.preview, "esc dismiss", "esc close")
	m = pressEnter(m)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("opening custom input moved focus to %q", got)
	}
	pending := onlyLanding(t, m)
	pending.probe.paneWorking = func() bool { return paneWorks(engine, "opencode", current) }
	lookForLanding(t, m)
	if got := focusedName(t, m); got != "ask" || m.latestSubmission.sessionID != "" {
		t.Fatal("custom input was treated as an answered question")
	}
	current = "Working on the supplied answer"
	lookForLanding(t, m)
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("resuming work left focus on %q", got)
	}
}
