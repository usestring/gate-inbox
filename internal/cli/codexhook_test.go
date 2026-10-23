package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// `hook codex UserPromptSubmit` for an adopted row's thread prints the
// board's steering, written for the gate-inbox command, once; the
// environment's session id -- the daemon's, in codex -- plays no part.
func TestRunHookCodexSteersAnAdoptedThreadOnce(t *testing.T) {
	dir := t.TempDir()
	const thread = "0190a000-0000-7000-8000-00000000000a"
	if err := hooks.NewManager(dir).SyncCodex([]hooks.CodexRow{{ID: "adoptedcx", Thread: thread, Adopted: true, Socket: "/s", Pane: "%1"}}); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"` + thread + `","prompt":"hi","cwd":"/w"}`
	var out bytes.Buffer
	if err := RunHook(strings.NewReader(payload), &out, []string{"codex", "UserPromptSubmit"}, "daemonrow", dir); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`"additionalContext"`, "gate-inbox spawn", "spawn_agent", "gate-inbox answer"} {
		if !strings.Contains(got, want) {
			t.Fatalf("steering lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "create_session") {
		t.Fatalf("the CLI steering names MCP tools codex does not have:\n%s", got)
	}
	out.Reset()
	if err := RunHook(strings.NewReader(payload), &out, []string{"codex", "UserPromptSubmit"}, "daemonrow", dir); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("second prompt printed %q", out.String())
	}
	if state, ok := hooks.NewManager(dir).Read("adoptedcx"); !ok || state != "working" {
		t.Fatalf("status = %q %v", state, ok)
	}
	if _, ok := hooks.NewManager(dir).Read("daemonrow"); ok {
		t.Fatal("the environment's session got the status")
	}
}
