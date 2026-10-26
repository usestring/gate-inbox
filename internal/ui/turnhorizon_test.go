package ui

import (
	"os"
	"testing"
)

func TestPaneModeReadsClaudeFooters(t *testing.T) {
	live, err := os.ReadFile("testdata/claude-live-frame.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, tool, clean, want string
		ok                      bool
	}{
		{"live frame", "claude", string(live), "auto", true},
		{"plan", "claude", "❯ \n  work | Opus 5.5 | 4%\n  ⏸ plan mode on (shift+tab to cycle)\n", "plan", true},
		{"manual", "claude", "  ⏸ manual mode on\n", "manual", true},
		{"shells", "claude", "  ⏵⏵ auto mode on · 1 shell\n", "auto", true},
		// The footer is the bottom-most: a mode quoted higher up loses.
		{"quoted above", "claude", "  ⏸ plan mode on\nsome output\n  ⏵⏵ auto mode on\n", "auto", true},
		{"no footer", "claude", "❯ \n", "", false},
		{"other tool", "codex", "  ⏸ plan mode on\n", "", false},
	} {
		got, ok := paneMode(c.tool, c.clean)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: paneMode = %q, %v, want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
