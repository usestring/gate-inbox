package dialog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readScreenFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestReadScreenReadsHeldDialogsWhole(t *testing.T) {
	cases := []struct {
		fixture string
		kind    ScreenKind
		text    []string
		choices []string
		cursor  int
	}{
		{
			fixture: "claude-2.1.284-w50-permission-bash.ansi",
			kind:    ScreenPermission,
			text:    []string{"Bash command", "curl -s https://example.com/ -o out.html", "Do you want to proceed?"},
			choices: []string{"Yes", "Yes, and don’t ask again for: curl *",
				"Yes, and switch to auto mode · auto mode handles these prompts for you", "No"},
			cursor: 0,
		},
		{
			fixture: "claude-2.1.284-w50-workspace-trust.ansi",
			kind:    ScreenWorkspaceTrust,
			text:    []string{"Accessing workspace:", "/home/dev/work/release-toolkits", "execute files here."},
			choices: []string{"No, exit", "Yes, I trust this folder"},
			cursor:  0,
		},
		{
			fixture: "claude-2.1.284-w50-mcp-trust.ansi",
			kind:    ScreenMCPTrust,
			text:    []string{"New MCP server found in this project:", "demo-files", "Use this MCP server"},
			choices: []string{"Use this MCP server", "Use this and all future MCP servers in this project",
				"Continue without using this MCP server"},
			cursor: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			screen, ok := ReadScreen(readScreenFixture(t, tc.fixture))
			if !ok {
				t.Fatal("no dialog read")
			}
			if screen.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", screen.Kind, tc.kind)
			}
			for _, want := range tc.text {
				if !strings.Contains(screen.Text(), want) {
					t.Errorf("text lacks %q:\n%s", want, screen.Text())
				}
			}
			var got []string
			for _, choice := range screen.Choices {
				got = append(got, choice.Label)
			}
			if strings.Join(got, "|") != strings.Join(tc.choices, "|") {
				t.Errorf("choices = %q, want %q", got, tc.choices)
			}
			if screen.Cursor() != tc.cursor {
				t.Errorf("cursor = %d, want %d", screen.Cursor(), tc.cursor)
			}
		})
	}
}

func TestReadScreenIgnoresAResolvedPane(t *testing.T) {
	if _, ok := ReadScreen(settledPane); ok {
		t.Error("a settled input line read as a dialog")
	}
}

func TestQuestionsReadAMultiSelectsTicks(t *testing.T) {
	questions := Questions(readScreenFixture(t, "claude-2.1.284-w50-stepper4-multiselect-ticked.ansi"), nil)
	if len(questions) != 4 {
		t.Fatalf("read %d questions, want 4", len(questions))
	}
	checks := questions[1]
	if !checks.MultiSelect || !checks.OnScreen {
		t.Fatalf("question 2 = %+v", checks)
	}
	ticked := map[string]bool{}
	for _, option := range checks.Options {
		ticked[option.Label] = option.Checked
	}
	if !ticked["Lint"] || ticked["Unit tests"] || ticked["Licence scan"] {
		t.Errorf("ticks = %v, want only Lint", ticked)
	}
}
