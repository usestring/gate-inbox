package status

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
)

// A pane narrower than an AskUserQuestion legend wraps it, and a legend read
// only whole left the dialog reading as idle. The inbox then pasted the next
// queued message into it, and the Enter that submits the paste ticked the box
// under the cursor -- captured live on Claude Code 2.1.296: a 45-column
// multi-select took "[✔] Build" from a message nobody typed into it.
func TestAWrappedQuestionLegendHoldsDelivery(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	frames := []struct {
		path string
		what string
	}{
		// "… · Esc" over "to cancel".
		{"testdata/claude-2.1.296-w40-multiselect-wrapped.txt", "a 40-column multi-select"},
		// A focused free-text row adds "ctrl+g to edit in nano", so the legend
		// wraps below 76 columns.
		{"testdata/claude-2.1.296-w72-multiselect-free-text-wrapped.txt", "a 72-column multi-select on its free-text row"},
		// Three wraps: every segment but the first starts a line of its own.
		{"testdata/claude-2.1.296-w20-stepper-multiselect-wrapped.txt", "a 20-column stepper multi-select"},
		// The review page has no legend; its question wraps instead, and the
		// cursor it opens on is "Submit answers", so an Enter submits them all.
		{"testdata/claude-2.1.296-w24-stepper-review-wrapped.txt", "a 24-column stepper review page"},
		{"../dialog/testdata/claude-2.1.283-tabs-w80-on-free-text.ansi", "an 80-column stepper on its free-text row"},
		// The separator itself opens the next line.
		{"../dialog/testdata/claude-2.1.283-tabs-w44-multiselect.ansi", "a 44-column stepper multi-select"},
		{"../dialog/testdata/claude-2.1.284-w50-stepper4-multiselect-ticked.ansi", "a 50-column stepper with a box ticked"},
		// A segment wraps inside itself: "n to" over "add notes".
		{"../dialog/testdata/claude-2.1.286-w40-preview.ansi", "a 40-column question with a preview"},
		// tmux keeps the space the wrap fell on: "Esc " over "to cancel".
		{"../dialog/testdata/claude-2.1.284-w40-approval-asked.txt", "a 40-column approval question"},
	}
	for _, f := range frames {
		t.Run(filepath.Base(f.path), func(t *testing.T) {
			raw, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatalf("read frame: %v", err)
			}
			pane := ansi.Strip(string(raw))
			if hold := engine.DeliveryHold("claude", pane); hold != Waiting {
				t.Fatalf("DeliveryHold = %q, want waiting: %s would take a queued message's Enter as its answer", hold, f.what)
			}
		})
	}
}

// Wrapping is the pane's, so legend wording split over lines must still be
// the legend's own: prose that merely mentions the keys keeps reading idle.
func TestAWrappedLegendNeedsBothEnds(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	for _, region := range []string{
		"● Enter to select · then\n  continue reading",
		"● The dialog says Enter to select · ↑/↓ to navigate · Esc\n  to cancel",
	} {
		if state, matched := engine.RuleMatch("claude", region+"\n❯ "); matched && state == Waiting {
			t.Fatalf("RuleMatch(%q) = waiting, want a resting prompt", region)
		}
	}
}
