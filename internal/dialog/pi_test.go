package dialog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The board's pi extension draws its permission dialog in Claude Code's
// shape. Captured from pi 1.5.1 at four widths: each is read as a guarded
// permission prompt, and a command the pane wrapped reads back exactly once
// the pending call is known.
func TestPiPermissionDialogReadsAsAGuardedPrompt(t *testing.T) {
	for _, width := range []int{60, 100, 140, 160} {
		raw, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("pi-1.5.1-w%d-permission-touch.txt", width)))
		if err != nil {
			t.Fatal(err)
		}
		pane := string(raw)
		command := fmt.Sprintf("touch a-rather-long-file-name-so-the-command-wraps-on-a-narrow-pane-w%d.txt", width)

		d, ok := Inspect(pane)
		if !ok || d.Kind != KindApproval || !d.Guarded() {
			t.Errorf("w%d: Inspect = %+v, %v; want a guarded %s", width, d, ok, KindApproval)
		}
		if _, ok := Parse(pane); ok {
			t.Errorf("w%d: Parse read the permission prompt as answerable", width)
		}

		screen, ok := ReadScreenFor("pi", pane)
		if !ok || screen.Kind != ScreenPermission {
			t.Fatalf("w%d: ReadScreenFor(pi) = %v, %v", width, screen.Kind, ok)
		}
		if screen.Title() != "Bash command" {
			t.Errorf("w%d: title %q", width, screen.Title())
		}
		labels := screen.Labels()
		want := []string{"Yes", "Yes, and don't ask again for: Bash(" + command + ":*)", "No, and tell pi what to do differently"}
		if len(labels) != 3 || labels[0] != want[0] || Compact(labels[1]) != Compact(want[1]) || labels[2] != want[2] {
			t.Errorf("w%d: labels %q", width, labels)
		}
		if screen.Cursor() != 0 {
			t.Errorf("w%d: cursor %d", width, screen.Cursor())
		}
		if got := screen.WithExact(command).Command(); got != command {
			t.Errorf("w%d: exact command %q", width, got)
		}
		if width == 60 && screen.Command() == command {
			t.Errorf("w60: the block was expected to wrap, so it reads back exactly only through WithExact")
		}
	}
}

// The extension's legend makes a permission prompt whatever the words above
// it say.
func TestReadPiScreenTakesTheExtensionLegendAsPermission(t *testing.T) {
	pane := "────────────────────\n Tool use\n\n │ mcp__x__y {}\n\n Pick one\n ❯ 1. Yes\n   2. No, and tell pi what to do differently\n\n Enter to confirm · Esc to cancel\n"
	screen, ok := ReadPiScreen(pane)
	if !ok || screen.Kind != ScreenPermission {
		t.Fatalf("ReadPiScreen = %v, %v", screen.Kind, ok)
	}
}
