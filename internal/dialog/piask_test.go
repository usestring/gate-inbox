package dialog

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func piCapture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The board's pi extension draws AskUserQuestion in Claude Code's shape, so
// the readers that know Claude's question read pi's unchanged. Captured from
// pi 1.5.1.
func TestPiQuestionDialogReadsAsClaudes(t *testing.T) {
	raw := piCapture(t, "pi-1.5.1-w80-ask-approval.ansi")
	plain := ansi.Strip(raw)
	d, ok := Inspect(plain)
	want := []string{"Approve", "Deny", "Type something.", "Chat about this"}
	if !ok || d.Kind != KindAsk || d.Guarded() || !slices.Equal(d.Options, want) || d.Cursor != 1 || d.FreeText != 3 {
		t.Fatalf("Inspect = %+v, %v", d, ok)
	}
	if d.Prompt != "May I delete the directory /tmp/sample-9000/scratchxxxxxx/w/cache?" {
		t.Errorf("prompt %q", d.Prompt)
	}
	if screen, ok := ReadScreenFor("pi", plain); !ok || screen.Kind != ScreenQuestion {
		t.Errorf("ReadScreenFor(pi) = %v, %v; want a question", screen.Kind, ok)
	}
}

// Several questions draw a row of tabs whose shown tab is marked by its
// background, and a multi-select draws its boxes and Submit row.
func TestPiQuestionTabsAndBoxesRead(t *testing.T) {
	first := piCapture(t, "pi-1.5.1-w80-ask-multiselect-first.ansi")
	d, ok := Inspect(ansi.Strip(first))
	if !ok || !d.MultiSelect || !d.SubmitRow || d.FreeText != 4 || len(d.Ticked) != 0 {
		t.Fatalf("multi-select = %+v, %v", d, ok)
	}
	stepper, ok := ParseStepper(first)
	if !ok || len(stepper.Steps) != 2 || stepper.Active != 0 || stepper.Steps[0].Label != "Toppings" {
		t.Fatalf("stepper on the first question = %+v, %v", stepper, ok)
	}
	stepper, ok = ParseStepper(piCapture(t, "pi-1.5.1-w80-ask-multiselect-second.ansi"))
	if !ok || stepper.Active != 1 || !stepper.Steps[0].Answered || stepper.Steps[1].Answered {
		t.Fatalf("stepper on the second question = %+v, %v", stepper, ok)
	}
	stepper, ok = ParseStepper(piCapture(t, "pi-1.5.1-w137-ask-tabs-first.ansi"))
	if !ok || len(stepper.Steps) != 2 || stepper.Active != 0 {
		t.Fatalf("stepper at 137 columns = %+v, %v", stepper, ok)
	}
}

// The record pi prints once its dialog closes is read as the child's own
// record of the answers it took.
func TestPiAnsweredRecordReads(t *testing.T) {
	for name, want := range map[string][]ReviewAnswer{
		"pi-1.5.1-w80-ask-after-submit.ansi": {
			{Question: "Which toppings do you want?", Answer: "Cheese, Peppers"},
			{Question: "May I order it now?", Answer: "Order"},
		},
		"pi-1.5.1-w137-ask-tabs-answered.ansi": {
			{Question: "Which rollout path should I take?", Answer: "Only region 4021"},
			{Question: "Should I notify the channel when it lands?", Answer: "Only after the deploy is verified in prod, then post"},
		},
	} {
		blocks := ParseAnswered(ansi.Strip(piCapture(t, name)))
		if len(blocks) == 0 || !slices.Equal(blocks[len(blocks)-1], want) {
			t.Errorf("%s: answered = %+v", name, blocks)
		}
	}
}
