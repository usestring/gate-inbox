package agentnudge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func payload(t *testing.T, tool, subagent, description, prompt, cwd string) []byte {
	t.Helper()
	input := map[string]string{"description": description, "prompt": prompt}
	if subagent != "" {
		input["subagent_type"] = subagent
	}
	raw, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": tool, "cwd": cwd, "tool_input": input,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

var now = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func paths(t *testing.T) Paths {
	return Paths{Dir: filepath.Join(t.TempDir(), "agent-nudge"), SessionID: "abcd1234", Home: t.TempDir()}
}

func hook(payload []byte, p Paths) (Verdict, string) { return Hook(payload, p, now) }

func writeAgent(t *testing.T, dir, file, body string) {
	t.Helper()
	agents := filepath.Join(dir, ".claude", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decision(t *testing.T, out string) (string, string) {
	t.Helper()
	var parsed struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("hook printed %q: %v", out, err)
	}
	if parsed.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Fatalf("hookEventName = %q", parsed.HookSpecificOutput.HookEventName)
	}
	return parsed.HookSpecificOutput.PermissionDecision, parsed.HookSpecificOutput.PermissionDecisionReason
}

// createCall pulls the create_session arguments back out of the reason.
func createCall(t *testing.T, reason string) map[string]string {
	t.Helper()
	_, rest, ok := strings.Cut(reason, "create_session with ")
	if !ok {
		t.Fatalf("reason has no create_session call: %q", reason)
	}
	var args map[string]string
	if err := json.NewDecoder(strings.NewReader(rest)).Decode(&args); err != nil {
		t.Fatalf("create_session call does not parse: %v in %q", err, rest)
	}
	return args
}

func TestReadOnlyBuiltinsRun(t *testing.T) {
	for _, subagent := range []string{"Explore", "Plan", "claude-code-guide", "statusline-setup"} {
		for _, tool := range []string{"Agent", "Task"} {
			if verdict, out := hook(payload(t, tool, subagent, "find the retry code", "where is it", "/work"), paths(t)); verdict != Allow || out != "" {
				t.Errorf("%s %s = %v %q, want it to run", tool, subagent, verdict, out)
			}
		}
	}
}

func TestOtherToolsAreNotTouched(t *testing.T) {
	if verdict, out := hook(payload(t, "Bash", "general-purpose", "x", "y", "/work"), paths(t)); verdict != Allow || out != "" {
		t.Fatalf("Bash = %v %q, want nothing", verdict, out)
	}
	if verdict, out := hook([]byte("not json"), paths(t)); verdict != Allow || out != "" {
		t.Fatalf("garbage = %v %q, want nothing", verdict, out)
	}
}

func TestWriteCapableCallIsSteeredWithACreateSessionCall(t *testing.T) {
	for _, subagent := range []string{"general-purpose", "", "fork", "linear-implement", "plugin:reviewer"} {
		p := paths(t)
		verdict, out := hook(payload(t, "Agent", subagent, "Fix the payments retry backoff!", "make the backoff exponential", "/work/repo"), p)
		if verdict != Steer {
			t.Fatalf("%q = %v, want Steer", subagent, verdict)
		}
		decided, reason := decision(t, out)
		if decided != "deny" {
			t.Fatalf("%q decision = %q, want deny", subagent, decided)
		}
		args := createCall(t, reason)
		want := map[string]string{"name": "fix-the-payments-retry", "prompt": "make the backoff exponential", "directory": "/work/repo"}
		for key, value := range want {
			if args[key] != value {
				t.Errorf("%q create_session %s = %q, want %q", subagent, key, args[key], value)
			}
		}
		for _, want := range []string{"list_sessions", "wait_for_session", "send the same Agent call again"} {
			if !strings.Contains(reason, want) {
				t.Errorf("%q reason lacks %q: %s", subagent, want, reason)
			}
		}
	}
}

// The deny is a nudge with a way out: the same call sent again runs.
func TestTheSameCallSentAgainRuns(t *testing.T) {
	p := paths(t)
	call := payload(t, "Agent", "general-purpose", "audit hooks", "read every hook", "/work")
	if verdict, _ := hook(call, p); verdict != Steer {
		t.Fatalf("first call = %v, want Steer", verdict)
	}
	if verdict, out := hook(call, p); verdict != Retry || out != "" {
		t.Fatalf("second call = %v %q, want it to run", verdict, out)
	}
	if verdict, _ := hook(payload(t, "Agent", "general-purpose", "audit hooks", "a different brief", "/work"), p); verdict != Steer {
		t.Fatalf("a new call = %v, want Steer", verdict)
	}
}

func TestNoSessionMeansNoDeny(t *testing.T) {
	for _, id := range []string{"", "../escape", "a.b"} {
		p := paths(t)
		p.SessionID = id
		if verdict, out := hook(payload(t, "Agent", "general-purpose", "x", "y", "/work"), p); verdict != Allow || out != "" {
			t.Fatalf("session %q = %v %q, want the call to run", id, verdict, out)
		}
	}
}

func TestStateOlderThanTheRetentionIsPruned(t *testing.T) {
	p := paths(t)
	call := payload(t, "Agent", "general-purpose", "audit hooks", "read every hook", "/work")
	if verdict, _ := hook(call, p); verdict != Steer {
		t.Fatalf("first call = %v, want Steer", verdict)
	}
	old := filepath.Join(p.Dir, "ffff0000-0123456789abcdef.md")
	if err := os.WriteFile(old, []byte("stale brief"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(old, now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if verdict, _ := Hook(payload(t, "Agent", "general-purpose", "other", "other brief", "/work"), p, now); verdict != Steer {
		t.Fatalf("second call = %v, want Steer", verdict)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("a week-old brief survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "abcd1234.steered")); err != nil {
		t.Fatalf("this session's state was pruned: %v", err)
	}
}

func TestThePromptReadsAsWritten(t *testing.T) {
	_, out := hook(payload(t, "Agent", "general-purpose", "html", "compare a < b && c > d", "/work"), paths(t))
	_, reason := decision(t, out)
	if !strings.Contains(reason, `"prompt":"compare a < b && c > d"`) {
		t.Fatalf("reason escapes the prompt: %s", reason)
	}
}

func TestALongPromptGoesInAFile(t *testing.T) {
	p := paths(t)
	brief := strings.Repeat("a long brief. ", 400)
	_, out := hook(payload(t, "Agent", "general-purpose", "big job", brief, "/work"), p)
	_, reason := decision(t, out)
	args := createCall(t, reason)
	if args["prompt"] != "" || args["prompt_file"] == "" {
		t.Fatalf("args = %v, want prompt_file and no inline prompt", args)
	}
	if got, err := os.ReadFile(args["prompt_file"]); err != nil || string(got) != brief {
		t.Fatalf("prompt_file holds %d bytes (%v), want the brief", len(got), err)
	}
	if strings.Contains(reason, "a long brief. a long brief.") {
		t.Fatal("reason repeats the long brief")
	}
}

func TestCustomAgentsAreClassifiedByTheirTools(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	writeAgent(t, project, "bq.md", "---\nname: bq-analyzer\ndescription: queries\ntools: Read, Bash, mcp__bigquery__*\n---\nbody\n")
	writeAgent(t, project, "impl.md", "---\nname: implementer\ntools: Read, Edit, Write, Bash\n---\n")
	writeAgent(t, project, "all.md", "---\nname: everything\ndescription: no tools field\n---\n")
	writeAgent(t, project, "listed.md", "---\nname: listed\ntools:\n  - Read\n  - Grep\n---\n")
	writeAgent(t, project, "minus.md", "---\nname: minus\ndisallowedTools: Edit, MultiEdit, Write, NotebookEdit, Agent, Task\n---\n")
	writeAgent(t, project, "rule.md", "---\nname: scoped-editor\ntools: Read, Edit(src/**), Bash(git log:*)\n---\n")
	writeAgent(t, home, "mine.md", "---\nname: home-reader\ntools: [Read, Glob]\n---\n")
	cwd := filepath.Join(project, "sub", "dir")
	cases := map[string]bool{
		"bq-analyzer": true, "implementer": false, "everything": false, "listed": true,
		"minus": true, "home-reader": true, "unknown-agent": false, "scoped-editor": false,
	}
	for name, want := range cases {
		if got := ReadOnly(name, cwd, home); got != want {
			t.Errorf("ReadOnly(%s) = %v, want %v", name, got, want)
		}
	}
}

func TestSessionName(t *testing.T) {
	for in, want := range map[string]string{
		"Review PR #12 for races":         "review-pr-12-for",
		"  ":                              "delegated-task",
		"Explore: go/web-access-mcp tree": "explore-go-web-access",
	} {
		if got := SessionName(in); got != want {
			t.Errorf("SessionName(%q) = %q, want %q", in, got, want)
		}
	}
}
