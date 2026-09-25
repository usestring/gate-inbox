package status

import (
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

// Every dialog claude puts up, captured off a real session rather than posed
// by hand, because both halves of the Left rule are read off the exact bytes
// tmux reports: a legend gains a segment or a dialog gains a decoration and
// the shipped patterns stop describing the screen.
//
// The pair asserted here is what frees Left. A dialog has to read as waiting
// -- selectionDialogUp will not touch a pane that does not -- and it has to
// not claim the arrows. Both were wrong on the two frames that carry a
// "←  ☐ … →" stepper, which is the one the operator meets most.
func TestEveryLiveClaudeDialogWaitsAndLeavesLeftAlone(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	frames := []struct {
		file string
		what string
	}{
		// "❯ No, exit" -- a marker with no number behind it, so only the
		// "Enter to confirm" rule speaks for this one.
		{"claude-trust-prompt", "the workspace trust prompt"},
		// One question, one answer: three legend segments and no stepper.
		{"claude-askuserquestion-single", "a single-select question"},
		// One question, several answers -- and claude draws the stepper even
		// here, for the one question plus Submit. Its legend still says the
		// vertical pair, and Left is a no-op in it (pressed on the live
		// dialog: the stepper does not move).
		{"claude-askuserquestion-multiselect", "a multi-select question"},
		// Several questions: five legend segments, and Tab is what steps
		// between them.
		{"claude-askuserquestion-multi", "a multi-question dialog"},
		// The review page every stepper dialog ends on. It draws no legend,
		// and its "❯ 1. Submit answers" is the activity cutoff, so only the
		// question it asks above the options says the dialog is still up.
		{"claude-askuserquestion-review", "a stepper dialog's review page"},
	}
	for _, f := range frames {
		t.Run(f.file, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + f.file + ".txt")
			if err != nil {
				t.Fatalf("read frame: %v", err)
			}
			pane := string(raw)
			state, matched := engine.RuleMatch("claude", pane)
			if !matched || state != Waiting {
				t.Fatalf("RuleMatch = (%v, %v), want waiting: %s is asking the operator something and reads as idle", state, matched, f.what)
			}
			if engine.DialogOwnsArrows("claude", pane) {
				t.Fatalf("%s claims the horizontal arrows, so Left cannot return to the list", f.what)
			}
		})
	}
}

// The legend is the only place a dialog says what the arrows do, so that is
// the only place to read it from. A pane's prose carries arrows far more
// often: "85% → 60%", "sample-repo#1430 → 95844f0", "one option → the other". 22 of
// the 63 panes live on the board when this was written held one, and the
// multi-select dialog above draws two of its own in a progress stepper.
func TestOnlyTheNavigateSegmentClaimsTheArrows(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	cases := []struct {
		name string
		rows []string
		want bool
	}{
		{"prose arrows above a permission prompt", []string{
			"  successful polls went ~97k/day → 12-18k",
			"  ⎿  sample-repo#1430 (abc-100001-sample-branch) → 95844f0",
			"",
			"  Do you want to proceed?",
			"❯ 1. Yes",
			"  2. No",
			"",
			"  Enter to confirm · Esc to cancel",
		}, false},
		{"a stepper above the options", []string{
			"←  ☐ Checks  ☐ Submit  →",
			"  Which checks should run before merge?",
			"❯ 1. [ ] Build",
			"",
			"  Enter to select · ↑/↓ to navigate · Esc to cancel",
		}, false},
		{"a legend naming the arrows", []string{
			"  Which approach?",
			"❯ 1. [ ] Resume the three fix rounds (Recommended)",
			"",
			"  Enter to select · Tab/Arrow keys to navigate · Esc to cancel",
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pane := strings.Join(c.rows, "\n") + "\n"
			if got := engine.DialogOwnsArrows("claude", pane); got != c.want {
				t.Fatalf("DialogOwnsArrows = %v, want %v", got, c.want)
			}
		})
	}
}
