package dialog

import (
	"strings"
	"testing"
)

// A 40-column Bash permission prompt as Claude Code 2.1.286 draws it: the
// command wrapped inside a path, then at a space, behind a gutter bar.
const narrowBashPrompt = "────────────────────────────────────────\n" +
	" Bash command\n" +
	" Run curl command to example.com\n" +
	"╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌\n" +
	" │ curl -sS -o /srv/example/build/ou\n" +
	" │ t/created.txt\n" +
	" │ https://example.com/\n" +
	"╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌\n" +
	" This command requires approval\n" +
	"\n" +
	" Do you want to proceed?\n" +
	" ❯ 1. Yes\n" +
	"   2. Yes, and don’t ask again for: curl\n" +
	"      *\n" +
	"   3. No\n" +
	"\n" +
	" Esc to cancel · Tab to amend\n"

const narrowBashCommand = "curl -sS -o /srv/example/build/out/created.txt https://example.com/"

func narrowBashScreen(t *testing.T) Screen {
	t.Helper()
	screen, ok := ReadScreen(narrowBashPrompt)
	if !ok || screen.Kind != ScreenPermission {
		t.Fatalf("read %v %q, want a permission prompt", ok, screen.Kind)
	}
	return screen
}

func TestPromptLeavesTheGutterOut(t *testing.T) {
	prompt := narrowBashScreen(t).Prompt()
	if strings.ContainsRune(prompt, gutterRune) {
		t.Fatalf("prompt %q carries the gutter bar", prompt)
	}
}

// The person approving the command reads it as it was written, not split
// wherever the pane's row ended.
func TestWithExactPutsTheCommandBackWhole(t *testing.T) {
	prompt := narrowBashScreen(t).WithExact("a different command", narrowBashCommand).Prompt()
	want := "Bash command Run curl command to example.com " + narrowBashCommand + " This command requires approval Do you want to proceed?"
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

// A transcript that has moved on to another call is not the dialog's.
func TestWithExactIgnoresAStringTheDialogDoesNotShow(t *testing.T) {
	screen := narrowBashScreen(t)
	if got, want := screen.WithExact("curl -sS https://example.com/other").Prompt(), screen.Prompt(); got != want {
		t.Fatalf("prompt = %q, want the screen's own %q", got, want)
	}
}

// A parent that copied a relay drawn with the gutter still asked the same
// question, so its user's answer still verifies.
func TestCompactIgnoresTheGutter(t *testing.T) {
	screen := narrowBashScreen(t)
	old := strings.Join(strings.Fields(strings.ReplaceAll(screen.Text(), "\n", " ")), " ")
	if !strings.Contains(Compact(old), Compact(screen.WithExact(narrowBashCommand).Prompt())) {
		t.Fatalf("Compact(%q) does not hold Compact(%q)", old, screen.WithExact(narrowBashCommand).Prompt())
	}
}
