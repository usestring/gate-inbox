package convo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeTurnOpen(t *testing.T) {
	prompt := func(text string) string {
		return `{"type":"user","message":{"role":"user","content":"` + text + `"}}`
	}
	assistant := func(stop string) string {
		return `{"type":"assistant","message":{"role":"assistant","stop_reason":"` + stop + `","content":[{"type":"text","text":"ok"}]}}`
	}
	toolResult := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"out"}]}}`
	attachment := `{"type":"attachment","attachment":{"type":"date"}}`
	sidechain := `{"type":"assistant","isSidechain":true,"message":{"stop_reason":"end_turn","content":[]}}`

	cases := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"a prompt sent on the command line, not answered yet", []string{prompt("Use the advance-goals skill for scoped goal x."), attachment}, true},
		{"a finished turn", []string{prompt("hi"), assistant("end_turn")}, false},
		{"a synthetic close after a resume", []string{prompt("hi"), assistant("stop_sequence")}, false},
		{"a tool is running", []string{prompt("hi"), assistant("tool_use")}, true},
		{"a tool result waiting on the model", []string{prompt("hi"), assistant("tool_use"), toolResult}, true},
		{"an interrupted turn", []string{prompt("hi"), assistant("tool_use"), prompt("[Request interrupted by user]")}, false},
		{"a slash command after a finished turn", []string{prompt("hi"), assistant("end_turn"), prompt("<command-name>/rename</command-name>"), prompt("<local-command-stdout>done</local-command-stdout>")}, false},
		{"a subagent finishing does not close the main turn", []string{prompt("hi"), assistant("tool_use"), sidechain}, true},
		{"no turn at all", []string{attachment}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(tc.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := ClaudeTurnOpen(path); got != tc.want {
				t.Fatalf("ClaudeTurnOpen = %v, want %v", got, tc.want)
			}
		})
	}
	if !ClaudeTurnOpen(filepath.Join(t.TempDir(), "missing.jsonl")) {
		t.Fatal("a missing transcript must count as open: there is nothing safe to resume")
	}
}
