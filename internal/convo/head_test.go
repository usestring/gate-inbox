package convo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHeadReadsTheOpeningPrompts(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseHead(raw)
	if len(got) != 1 || got[0] != "open the thing" {
		t.Fatalf("first prompts = %q", got)
	}
}

// Tool results, harness reminders, slash expansions and meta records all
// arrive as user records; none of them was typed.
func TestParseHeadSkipsWhatNobodyTyped(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"Caveat: the messages below"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>x</system-reminder>"},{"type":"text","text":"first real one"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"hi"}}`,
		`{"type":"user","message":{"role":"user","content":"[gate-inbox] Name this session"}}`,
		`{"type":"user","message":{"role":"user","content":"second"}}`,
		`{"type":"user","message":{"role":"user","content":"third"}}`,
		`{"type":"user","message":{"role":"user","content":"fourth"}}`,
	}, "\n")
	got := parseHead([]byte(raw))
	want := []string{"first real one", "second", "third"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("first prompts = %q, want %q", got, want)
	}
}

// A session opened with a skill's slash command has no other record of what
// it was started for, so the command line stands as its opening prompt. A
// built-in is followed by its output rather than an expansion and is not one.
func TestParseHeadKeepsTheSkillCommandASessionOpenedWith(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"<command-name>/mcp</command-name>\n<command-message>mcp</command-message>\n<command-args>string-web-access</command-args>"}}`,
		`{"type":"user","message":{"role":"user","content":"<local-command-stdout>Connected.</local-command-stdout>"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-message>triage-gmail-inbox</command-message>\n<command-name>/triage-gmail-inbox</command-name>"}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /x"}]}}`,
		`{"type":"user","message":{"role":"user","content":"<command-message>advance-goals</command-message>\n<command-name>/advance-goals</command-name>\n<command-args>for goal x</command-args>"}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"# Advance Goals"}}`,
		`{"type":"user","message":{"role":"user","content":"typed"}}`,
	}, "\n")
	got := parseHead([]byte(raw))
	want := []string{"/triage-gmail-inbox", "/advance-goals for goal x", "typed"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("first prompts = %q, want %q", got, want)
	}
}

// A head short of the count is read again once the file grows; a full one
// is never read again.
func TestReadHeadRereadsOnlyWhileShort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	line := func(s string) string { return `{"type":"user","message":{"role":"user","content":"` + s + `"}}` + "\n" }
	if err := os.WriteFile(path, []byte(line("one")), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := New("", "")
	var cost Cost
	info, _ := os.Stat(path)
	if got := ix.readHead(path, info.Size(), &cost); len(got) != 1 {
		t.Fatalf("first read = %q", got)
	}
	if got := ix.readHead(path, info.Size(), &cost); cost.Heads != 1 || len(got) != 1 {
		t.Fatalf("an unchanged file was read again: heads=%d", cost.Heads)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(line("two") + line("three"))
	f.Close()
	info, _ = os.Stat(path)
	if got := ix.readHead(path, info.Size(), &cost); cost.Heads != 2 || len(got) != 3 {
		t.Fatalf("a grown file was not read again: heads=%d prompts=%q", cost.Heads, got)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(line("four"))
	f.Close()
	info, _ = os.Stat(path)
	if got := ix.readHead(path, info.Size(), &cost); cost.Heads != 2 || len(got) != 3 {
		t.Fatalf("a complete head was read again: heads=%d prompts=%q", cost.Heads, got)
	}
}
