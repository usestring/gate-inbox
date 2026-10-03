package dialog

import (
	"fmt"
	"strings"
	"testing"
)

// The dialogs Claude Code 2.1.286 draws for permission and trust, captured on
// live panes at 40, 50 and 60 columns.
func TestReadScreenReads2286DialogsAtEveryWidth(t *testing.T) {
	cases := []struct {
		name    string
		kind    ScreenKind
		prompt  []string
		choices []string
		cursor  int
	}{
		{"permission-bash", ScreenPermission, []string{"Bash command", "touch perm-probe.txt", "Do you want to proceed?"},
			[]string{"Yes", "Yes, and always allow access to", "No"}, 0},
		{"permission-webfetch", ScreenPermission, []string{"Fetch", "url: https://example.com/", "Do you want to allow Claude to fetch"},
			[]string{"Yes", "Yes, and don't ask again for example.com", "No, and tell Claude what to do differently (esc)"}, 0},
		{"workspace-trust", ScreenWorkspaceTrust, []string{"Accessing workspace:", "Quick safety check", "execute files here."},
			[]string{"No, exit", "Yes, I trust this folder"}, 0},
		{"mcp-trust", ScreenMCPTrust, []string{"New MCP server found in this project: probe", "All tool calls require approval."},
			[]string{"Use this MCP server", "Use this and all future MCP servers in this project",
				"Continue without using this MCP server"}, 2},
	}
	for _, tc := range cases {
		for _, width := range []int{40, 50, 60} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, width), func(t *testing.T) {
				screen, ok := ReadScreen(readScreenFixture(t, fmt.Sprintf("claude-2.1.286-w%d-%s.ansi", width, tc.name)))
				if !ok {
					t.Fatal("no dialog read")
				}
				if screen.Kind != tc.kind {
					t.Errorf("kind = %q, want %q", screen.Kind, tc.kind)
				}
				for _, want := range tc.prompt {
					if !strings.Contains(Compact(screen.Prompt()), Compact(want)) {
						t.Errorf("prompt lacks %q:\n%s", want, screen.Prompt())
					}
				}
				labels := screen.Labels()
				if len(labels) != len(tc.choices) {
					t.Fatalf("choices = %q, want %q", labels, tc.choices)
				}
				for i, want := range tc.choices {
					if !strings.HasPrefix(Compact(labels[i]), Compact(want)) {
						t.Errorf("choice %d = %q, want it to start %q", i+1, labels[i], want)
					}
				}
				if screen.Cursor() != tc.cursor {
					t.Errorf("cursor = %d, want %d", screen.Cursor(), tc.cursor)
				}
			})
		}
	}
}

// The command sits between two dashed rules on a 2.1.286 permission prompt.
// Read as the dialog's top edge, the lower one cut the tool and the command
// out of what the parent is shown and what its user is asked.
func TestADashedRuleIsNotTheDialogsTopEdge(t *testing.T) {
	screen, ok := ReadScreen(readScreenFixture(t, "claude-2.1.286-w50-permission-read.ansi"))
	if !ok {
		t.Fatal("no dialog read")
	}
	for _, want := range []string{"Read file", "Read(/tmp/claude-1000/gie287/measure/outside/notes-50.txt)", "Do you want to proceed?"} {
		if !strings.Contains(Compact(screen.Prompt()), Compact(want)) {
			t.Errorf("prompt lacks %q:\n%s", want, screen.Prompt())
		}
	}
	if strings.Contains(screen.Text(), "╌") {
		t.Errorf("the dashed rules are kept as text:\n%s", screen.Text())
	}
}

// A numbered list the child printed above its permission prompt is the turn,
// not the dialog's choices.
func TestANumberedListAboveAPermissionPromptIsNotItsChoices(t *testing.T) {
	prose := "  1. Yes, delete everything\n  2. Keep the cache\n  3. No\n\n"
	for _, width := range []int{40, 50, 60} {
		for _, name := range []string{"permission-bash", "permission-webfetch"} {
			pane := prose + readScreenFixture(t, fmt.Sprintf("claude-2.1.286-w%d-%s.ansi", width, name))
			screen, ok := ReadScreen(pane)
			if !ok {
				t.Fatalf("%s/%d: no dialog read", name, width)
			}
			if screen.Choose("Yes, delete everything") != 0 || screen.Choose("Keep the cache") != 0 {
				t.Errorf("%s/%d: the prose list was read as choices: %q", name, width, screen.Labels())
			}
			if got := screen.Choose("No"); got != len(screen.Choices) {
				t.Errorf("%s/%d: No is choice %d of %q", name, width, got, screen.Labels())
			}
		}
	}
}

func TestScreenChooseIsByTextNeverByPosition(t *testing.T) {
	screen, _ := ReadScreen(readScreenFixture(t, "claude-2.1.286-w60-permission-webfetch.ansi"))
	for answer, want := range map[string]int{
		"Yes": 1, "yes, and don't ask again for example.com": 2, "No, and tell Claude what to do differently": 3,
		"1": 0, "2": 0, "Maybe": 0,
	} {
		if got := screen.Choose(answer); got != want {
			t.Errorf("Choose(%q) = %d, want %d", answer, got, want)
		}
	}
}

// A worker's numbered prose at rest ends on its input line, never on a
// numbered choice with the marker on it.
func TestNumberedProseWithNoMarkerIsNotALegendlessDialog(t *testing.T) {
	pane := strings.Repeat("─", 50) + "\n  1. First\n  2. Second\n  3. Third\n"
	if _, ok := ReadScreen(pane); ok {
		t.Fatal("unmarked numbered prose read as a dialog")
	}
	pane = "  1. First\n❯ 2. Second\n  3. Third\n"
	if _, ok := ReadScreen(pane); ok {
		t.Fatal("a marked list with no top edge read as a dialog")
	}
}
