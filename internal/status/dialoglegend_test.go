package status

import (
	"os"
	"strings"
	"testing"
)

// A live pane: a turn mid-run, spinner and a running shell command on screen,
// whose diff output quotes the dialog legend. The fixture is the falsifier for
// the rule -- a legend is read by where the phrase sits, not that it appears.
func TestAQuotedLegendInsideATurnIsNotWaiting(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-turn-quoting-a-dialog-legend.txt")
	if err != nil {
		t.Fatal(err)
	}
	pane := string(raw)
	if !strings.Contains(pane, `"Enter to confirm"`) {
		t.Fatal("fixture no longer quotes the legend it exists for")
	}
	engine := defaultEngine(t)
	if got, _ := engine.Match("claude", pane); got != Working {
		t.Fatalf("a turn quoting a dialog legend read as %q, want %q", got, Working)
	}
}

// The shapes the rule does have to keep reading: the legend as Claude draws it,
// with or without a lead-in segment, boxed or bare.
func TestDialogLegendsStillRead(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name string
		pane string
		want string
	}{
		{"legend with a cancel segment",
			" ❯ 1. Yes, I trust this folder\n   2. No, exit\n Enter to confirm · Esc to cancel", Waiting},
		{"legend alone on its line",
			"Do you want to proceed?\n  1. Yes\n  2. No\n Enter to confirm\n❯ ", Waiting},
		{"legend after a select segment",
			"Run rm build.tmp?\n  Allow once\n  Deny\n  ↑/↓ to select, Enter to confirm\n", Waiting},
		{"legend inside a box rule",
			"│ Run rm build.tmp?              │\n│ Enter to confirm · Esc to cancel │\n", Waiting},
		{"prose naming the legend mid-sentence",
			"● I will press Enter to confirm the dialog once you answer it.\n" +
				"✻ Herding… (4s · ↓ 1.2k tokens)\n", Working},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := engine.Match("claude", tc.pane); got != tc.want {
				t.Fatalf("status %q, want %q", got, tc.want)
			}
		})
	}
}
