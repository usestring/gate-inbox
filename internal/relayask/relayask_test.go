package relayask

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
)

// derivePrompt is Claude Code 2.1.286's permission prompt for derive at 80
// columns, the command wrapped between its dashed rules.
func derivePrompt(t *testing.T) dialog.Screen {
	t.Helper()
	var b strings.Builder
	b.WriteString(strings.Repeat("─", 80) + "\n Bash command\n Replay derive serially under a memory cap\n")
	b.WriteString(strings.Repeat("╌", 80) + "\n")
	for rest := derive; rest != ""; {
		n := min(78, len(rest))
		b.WriteString(" " + rest[:n] + "\n")
		rest = rest[n:]
	}
	b.WriteString(strings.Repeat("╌", 80) + "\n Do you want to proceed?\n ❯ 1. Yes\n   2. No\n\n Esc to cancel · Tab to amend\n")
	screen, ok := dialog.ReadScreen(b.String())
	if !ok || screen.Kind != dialog.ScreenPermission {
		t.Fatalf("not read as a permission prompt: %+v", screen)
	}
	return screen.WithExact(derive, "Replay derive serially under a memory cap")
}

func TestAPermissionPromptIsRelayedAsADecision(t *testing.T) {
	screen := derivePrompt(t)
	if screen.Command() != derive {
		t.Fatalf("command %q", screen.Command())
	}
	in := ForScreen(screen, Screen{Child: "store-split-probe", Description: "Replay derive serially under a memory cap"})
	if in == nil || len(in.Questions) != 1 {
		t.Fatalf("input %+v", in)
	}
	q := in.Questions[0]
	if q.Header != dialog.ApprovalHeader {
		t.Errorf("header %q", q.Header)
	}
	if !strings.HasPrefix(q.Question, `store-split-probe wants to run a Bash command: "Replay derive serially under a memory cap". Seen in the command: deletes scratchpad/out/*;`) {
		t.Errorf("question does not lead with who and the description:\n%s", q.Question)
	}
	if strings.Contains(q.Question, "systemd-run") || strings.Contains(q.Question, "job-1") || len(q.Question) > 400 {
		t.Errorf("question carries the command (%d chars):\n%s", len(q.Question), q.Question)
	}
	if !strings.Contains(q.Question, screen.PromptToken()) {
		t.Errorf("question does not cite %s", screen.PromptToken())
	}
	if len(q.Options) != 2 || q.Options[0].Label != "Yes" || q.Options[1].Label != "No" {
		t.Fatalf("options %+v", q.Options)
	}
	for _, option := range q.Options {
		if !strings.Contains(option.Preview, derive) || option.Description == "" {
			t.Errorf("option %q: preview %q, description %q", option.Label, option.Preview, option.Description)
		}
	}
}

// With no transcript to read, the description is the one the prompt draws.
func TestAPermissionPromptFallsBackToTheDescriptionItDraws(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", "claude-2.1.286-w60-permission-bash.ansi"))
	if err != nil {
		t.Fatal(err)
	}
	screen, ok := dialog.ReadScreen(ansi.Strip(string(raw)))
	if !ok {
		t.Fatal("no dialog")
	}
	if screen.Command() != "touch perm-probe.txt" || screen.Description() != "Create empty file perm-probe.txt" {
		t.Fatalf("command %q, description %q", screen.Command(), screen.Description())
	}
	q := ForScreen(screen, Screen{}).Questions[0]
	if !strings.HasPrefix(q.Question, `The child wants to run a Bash command: "Create empty file perm-probe.txt". Seen in the command: writes perm-probe.txt; names nothing it deletes;`) {
		t.Errorf("question %s", q.Question)
	}
	if len(q.Options) != 3 || !strings.Contains(q.Options[1].Preview, "touch perm-probe.txt") {
		t.Errorf("options %+v", q.Options)
	}
}

// A dialog with no command block keeps its text as the question.
func TestATrustDialogIsStillRelayedWordForWord(t *testing.T) {
	screen := dialog.Screen{Kind: dialog.ScreenWorkspaceTrust, Lines: []string{"Do you trust the contents of this folder?"},
		Choices: []dialog.ScreenChoice{{Label: "Yes, I trust this folder"}, {Label: "No, exit"}}}
	q := ForScreen(screen, Screen{Child: "probe"}).Questions[0]
	if q.Question != screen.Prompt() || q.Options[0].Preview != "" {
		t.Errorf("question %+v", q)
	}
}
