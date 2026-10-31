package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/launch"
)

const keepAlive = "[gate-inbox] Prompt-cache keep-alive, not a task: reply only ok, no tools."

func TestClassifyTurn(t *testing.T) {
	for text, want := range map[string]Turn{
		"fix the flaky test": TurnWork,
		keepAlive:            TurnChore,
		"<pasted_content id=\"x\">\n" + keepAlive + "\n</pasted_content>":                             TurnChore,
		"Auto wake-up: the previous turn died on a transient API error. Continue where you left off.": TurnNudge,
		"[gate-inbox] The reported usage-limit reset time has passed. Continue the interrupted task.": TurnNudge,
		"[gate-inbox] From agent \"ci-fix\" (session ab12), sent 10:02. Please rebase.":               TurnWork,
	} {
		if got := ClassifyTurn(text); got != want {
			t.Errorf("ClassifyTurn(%.40q) = %d, want %d", text, got, want)
		}
	}
}

func TestWorkMessagesReadsPastKeepAlive(t *testing.T) {
	before := []Message{{"user", "ship it"}, {"assistant", "Merged the PR."}}
	after := append(append([]Message{}, before...), Message{"user", keepAlive}, Message{"assistant", "ok"})
	got := WorkMessages(after)
	if len(got) != len(before) || got[1] != before[1] {
		t.Fatalf("WorkMessages = %v, want %v", got, before)
	}
	nudged := append(append([]Message{}, before...),
		Message{"user", "Auto wake-up: the previous turn died. Continue where you left off."}, Message{"assistant", "Picked up the deploy."})
	if got := WorkMessages(nudged); len(got) != 3 || got[2].Text != "Picked up the deploy." {
		t.Fatalf("a nudge's reply is the work: %v", got)
	}
}

func TestWorkMessagesReadsPastRename(t *testing.T) {
	for name, directive := range map[string]string{
		"deferred": launch.DeferredRenameDirective,
		"adopted":  launch.AdoptedRenameDirective("gate-inbox rename"),
	} {
		t.Run(name, func(t *testing.T) {
			messages := []Message{
				{"user", "ship it"}, {"assistant", "Working on it."},
				{"user", keepAlive}, {"assistant", "ok"},
				{"user", directive}, {"assistant", "Merged the PR."},
			}
			got := WorkMessages(messages)
			if len(got) != 3 || got[2] != messages[5] {
				t.Fatalf("WorkMessages = %v, want the resumed work reply after renaming", got)
			}
		})
	}
}

func TestReadStatusReport(t *testing.T) {
	report := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"mcp__gate-inbox__report_status","input":{"state":"done","note":"PR awaits review"}}]}}`
	result := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"status recorded: done"}]}}`
	prompt := func(text string) string {
		return `{"type":"user","message":{"role":"user","content":` + quote(text) + `}}`
	}
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"reported", []string{prompt("ship it"), report, result}, "done"},
		{"across a keep-alive", []string{prompt("ship it"), report, result, prompt(keepAlive)}, "done"},
		{"lapses on a new prompt", []string{prompt("ship it"), report, result, prompt("now the docs")}, ""},
		{"lapses on an image-only prompt", []string{prompt("ship it"), report, result,
			`{"type":"user","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":""}}]}}`}, ""},
		{"none", []string{prompt("ship it")}, ""},
	} {
		path := filepath.Join(t.TempDir(), "c.jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(tc.lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, found, err := ReadStatusReport(Target{Tool: ToolClaude, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if found != (tc.want != "") || got.State != tc.want {
			t.Errorf("%s: got %+v found=%v, want %q", tc.name, got, found, tc.want)
		}
	}
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}
