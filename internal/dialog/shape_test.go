package dialog

import (
	"reflect"
	"strings"
	"testing"
)

// The Codex panes below are the fixtures internal/status already reads these
// three dialogs from, carried over rather than rewritten: the status rules and
// this parser are two readings of one screen, and a shape that drifts between
// them is a session the board calls waiting and this calls nothing at all.

// codexApprovalPane is Codex asking whether it may run a command.
const codexApprovalPane = `  $ echo hello world

› 1. Yes, proceed (y)
  2. Yes, and don't ask again for commands that start with ` + "`echo hello world`" + ` (p)
  3. No, and tell Codex what to do differently (esc)

  Press enter to confirm or esc to cancel
`

// codexTrustPane is Codex's first run in a directory.
const codexTrustPane = `Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection.

› 1. Yes, continue
  2. No, quit

  Press enter to continue
`

// codexAskPane is request_user_input, which is Codex's AskUserQuestion: the
// worker asking its caller about the work, and the one Codex dialog that is
// not a question about this program's own authority.
const codexAskPane = `  Choose an option.

  › 1. Option 1  First choice.
    2. Option 2  Second choice.

  tab to add notes | enter to submit answer | esc to interrupt
`

// The shape that stalled the fleet: numbered options with no "Enter to select"
// legend over them, which read as no dialog at all.
func TestCodexQuestionDialogIsAnswerable(t *testing.T) {
	dialog, ok := Parse(codexAskPane)
	if !ok {
		t.Fatal("a captured request_user_input pane did not parse")
	}
	if dialog.Kind != KindCodexAsk {
		t.Fatalf("kind = %q, want %q", dialog.Kind, KindCodexAsk)
	}
	want := []string{"Option 1  First choice.", "Option 2  Second choice."}
	if !reflect.DeepEqual(dialog.Options, want) {
		t.Fatalf("options = %q, want %q", dialog.Options, want)
	}
	// Codex marks its selection with a light angle where Claude Code uses a
	// heavy one; reading only the heavy one left the cursor at 0, which is
	// refused even once the legend is recognised.
	if dialog.Cursor != 1 {
		t.Fatalf("cursor = %d, want the marked option", dialog.Cursor)
	}
	if why := dialog.Refusal(); why != "" {
		t.Fatalf("a plain question dialog is refused: %s", why)
	}
	keys, err := AnswerKeys(dialog, "Option 2")
	if err != nil {
		t.Fatalf("AnswerKeys: %v", err)
	}
	if want := []string{"Down", "Enter"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

// Read, and still the human's. Both halves matter: the first is what lets a
// refusal name the shape, and the second is the boundary the refusal is about.
func TestTheGuardedDialogsAreReadAndStillRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		pane string
		kind Kind
		says string
	}{
		{"a Claude permission prompt", capturedPermissionPane, KindApproval, "permission prompt"},
		{"a Codex command approval", codexApprovalPane, KindApproval, "permission prompt"},
		{"Codex's first-run trust dialog", codexTrustPane, KindCodexTrust, "directory-trust"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialog, ok := Inspect(tc.pane)
			if !ok {
				t.Fatal("Inspect read nothing; a refusal cannot name what it did not see")
			}
			if dialog.Kind != tc.kind {
				t.Fatalf("kind = %q, want %q", dialog.Kind, tc.kind)
			}
			if !dialog.Guarded() {
				t.Fatal("a dialog the human owns did not read as guarded")
			}
			if why := dialog.Refusal(); !strings.Contains(why, tc.says) {
				t.Fatalf("refusal = %q, want it to name the shape (%q)", why, tc.says)
			}
			// And the callers that act on a dialog still see nothing here, so
			// widening the parser did not widen what gets answered.
			if _, ok := Parse(tc.pane); ok {
				t.Fatal("Parse offered a guarded dialog to be answered")
			}
		})
	}
}

// Numbered lines with no legend over them are a worker's prose or an
// operator's half-typed draft, and Codex's input line wears the same marker as
// its dialogs. The legend is the whole guard against reading one as the other.
func TestNumberedLinesWithNoLegendAreNotADialog(t *testing.T) {
	for name, pane := range map[string]string{
		"a numbered draft in Codex's input box": "• Working (0s • esc to interrupt)\n\n" +
			"› 1. keep this as ordinary input\n  gpt-5.6-terra medium · /home/dev\n",
		"a numbered list in a worker's answer": "• Two things to do next:\n\n" +
			"  1. Rebuild the corpus\n  2. Re-run the oracle\n\n" +
			"─ Worked for 2m 05s ───────\n\n› Ask Codex to do anything\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := Inspect(pane); ok {
				t.Fatal("read as a dialog; keys would go to a pane that takes them as text")
			}
		})
	}
}

// Four refusals, four different things to do about them, and for four days
// they were one sentence. Two send the caller to a person and two mean look
// again in a moment; a caller told only "cannot be answered" does neither.
func TestEveryRefusalNamesItsOwnShape(t *testing.T) {
	seen := map[string]bool{}
	for _, tc := range []struct {
		name   string
		dialog Dialog
		says   string
	}{
		{"a permission prompt", Dialog{Kind: KindApproval, Cursor: 1}, "permission prompt"},
		{"a trust prompt", Dialog{Kind: KindCodexTrust, Cursor: 1}, "directory-trust"},
		{"a multi-select", Dialog{Kind: KindAsk, MultiSelect: true, Cursor: 1}, "multi-select"},
		{"an unknown cursor", Dialog{Kind: KindAsk}, "selection this cannot locate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := tc.dialog.Refusal()
			if !strings.Contains(why, tc.says) {
				t.Fatalf("refusal = %q, want it to say %q", why, tc.says)
			}
			if seen[why] {
				t.Fatalf("refusal %q is shared with another shape", why)
			}
			seen[why] = true
			if _, err := AnswerKeys(tc.dialog, "Yes"); err != ErrNotKeyAnswerable {
				t.Fatalf("AnswerKeys = %v, want ErrNotKeyAnswerable", err)
			}
		})
	}
}

// The legend a pane's options belong to is the one nearest them, and a pane
// carries its own scrollback: a settled permission prompt above a live
// question would otherwise decide the reading and guard a dialog that is
// answerable.
func TestTheLiveLegendDecidesRatherThanTheOldestOne(t *testing.T) {
	pane := capturedPermissionPane + "\n" + capturedAskPane
	dialog, ok := Inspect(pane)
	if !ok {
		t.Fatal("no dialog")
	}
	if dialog.Kind != KindApproval {
		t.Fatalf("kind = %q, want the topmost legend on the pane", dialog.Kind)
	}
	flipped := capturedAskPane + "\n" + capturedPermissionPane
	dialog, ok = Inspect(flipped)
	if !ok {
		t.Fatal("no dialog")
	}
	if dialog.Kind != KindAsk {
		t.Fatalf("kind = %q, want the topmost legend on the pane", dialog.Kind)
	}
}
