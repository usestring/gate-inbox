package extension_test

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

const askPane = "Which region should the survey cover?\n\n" +
	"\x1b[36m❯ 1. North only\x1b[0m\n" +
	"  2. South only\n\n" +
	"\x1b[2mEnter to select · ↑/↓ to navigate · Esc to cancel\x1b[0m\n"

const permissionPane = `● Bash(rm -rf build/)

  Do you want to proceed?
  ❯ 1. Yes
    2. No, and tell Claude what to do differently

  Enter to confirm · Esc to cancel
`

// A capture taken with its escapes reads as one taken without: the legend is
// drawn dimmed, and an unstripped one matched nothing.
func TestParseDialogReadsACaptureWithEscapes(t *testing.T) {
	held, ok := extension.ParseDialog(askPane)
	if !ok {
		t.Fatal("ParseDialog found no dialog on an AskUserQuestion pane")
	}
	if held.Kind != extension.DialogAsk || held.Cursor != 1 || held.Guarded() || held.Refusal() != "" {
		t.Fatalf("read %+v, want an answerable AskUserQuestion on option 1", held)
	}
	if got := strings.Join(held.Options, "|"); got != "North only|South only" {
		t.Fatalf("options = %q", got)
	}
	if held.Choose("south only") != 2 || held.Choose("cover both, then stop") != 0 {
		t.Fatalf("Choose picked the wrong options on %v", held.Options)
	}
	if q := held.Question(); !strings.Contains(q, "Which region") || !strings.Contains(q, "2. South only") {
		t.Fatalf("Question() = %q", q)
	}
	if held.Standing() != 1 {
		t.Fatalf("Standing() = %d, want 1", held.Standing())
	}
}

// A permission prompt is a person's: Parse does not offer it, and Inspect
// reads it so the refusal can say what it is.
func TestInspectDialogKeepsTheGuardedKinds(t *testing.T) {
	if _, ok := extension.ParseDialog(permissionPane); ok {
		t.Fatal("ParseDialog offered a permission prompt as answerable")
	}
	held, ok := extension.InspectDialog(permissionPane)
	if !ok || held.Kind != extension.DialogApproval || !held.Guarded() {
		t.Fatalf("InspectDialog = %+v, %v; want a guarded permission prompt", held, ok)
	}
	if !strings.Contains(held.Refusal(), "permission prompt") {
		t.Fatalf("Refusal() = %q", held.Refusal())
	}
}

func TestParseDialogFindsNoneInProse(t *testing.T) {
	if _, ok := extension.InspectDialog("1. first\n2. second\nall done\n"); ok {
		t.Fatal("a numbered list with no legend read as a dialog")
	}
}

// The value is a copy: changing its options changes nothing a later read
// sees.
func TestDialogIsACopy(t *testing.T) {
	held, _ := extension.ParseDialog(askPane)
	held.Options[0] = "changed"
	again, _ := extension.ParseDialog(askPane)
	if again.Options[0] != "North only" {
		t.Fatalf("a second read saw %q", again.Options[0])
	}
}
