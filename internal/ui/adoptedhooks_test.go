package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/singleton"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// fakeClaudeScript stands in for a claude somebody started in their own tmux
// pane: it records its pid, waits to be told to go, then runs each queued hook
// command the way Claude Code does -- sh -c, as its own child, with the pane's
// environment and the payload on stdin -- and then idles.
const fakeClaudeScript = `echo $$ > "$1/pid"
while [ ! -f "$1/go" ]; do sleep 0.05; done
for run in "$1"/run.*; do sh -c "$(cat "$run/cmd")" < "$run/payload" >> "$1/out" 2>&1; done
touch "$1/done"
exec cat
`

func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return string(raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

// waitForPath waits for a file that may be empty.
func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

// globalCommand reads back from a settings file the one command Claude Code
// would run for event.
func globalCommand(t *testing.T, settings, event string) string {
	t.Helper()
	var parsed struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	groups := parsed.Hooks[event]
	if len(groups) != 1 || groups[0].Matcher != "" || len(groups[0].Hooks) != 1 {
		t.Fatalf("%s: want one matcher-less entry, got %+v", event, groups)
	}
	return groups[0].Hooks[0].Command
}

// A claude started outside the board, with nothing but the global hooks in
// its user settings, is adopted and from then on reports as its row: status
// into the row's log through the launch's own hook commands. Once
// the board lets the pane go, its hooks go quiet again.
func TestAnAdoptedOutsideClaudeReportsThroughTheGlobalHooks(t *testing.T) {
	// An outside claude carries none of a launch's environment; a test run
	// from inside a managed session would otherwise hand its own to the pane.
	for _, name := range []string{hooks.EnvStatusFile, hooks.EnvSessionID, hooks.EnvExecutable} {
		t.Setenv(name, "")
	}
	m := buildModel(t)
	claudeHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	configDir := m.hooks.ConfigDir()

	work := t.TempDir()
	script := filepath.Join(work, "claude.sh")
	if err := os.WriteFile(script, []byte(fakeClaudeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	socket, pane := uiForeignServer(t, "sh "+script+" "+work)
	pid, err := strconv.Atoi(strings.TrimSpace(waitForFile(t, filepath.Join(work, "pid"))))
	if err != nil {
		t.Fatal(err)
	}
	writeClaudeConversation(t, claudeHome, int32(pid), work, "outside work")

	const id = "outside1"
	if err := m.store.CreateSession(store.Session{
		ID: id, Name: "outside", Tool: "claude-hooked", Cwd: work,
		Status: status.Idle, CreatedAt: time.Now(), LastStatusAt: time.Now(),
		TmuxSocket: socket, TmuxPaneID: pane,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := m.tmux.Adopt(id, tmux.Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if m.sessions, err = m.store.ListSessions(false); err != nil {
		t.Fatal(err)
	}

	m.syncAdoptedHooks(time.Now())
	markers, _ := os.ReadDir(m.hooks.AdoptedDir())
	if len(markers) != 1 {
		t.Fatalf("adopted markers = %v, want one for the pane", markers)
	}
	if got := waitForFile(t, filepath.Join(m.hooks.AdoptedDir(), markers[0].Name())); got != fmt.Sprintf("%s %d\n", id, pid) {
		t.Fatalf("marker = %q, want the row and the claude's pid", got)
	}

	// The board is up, and the binary the hooks call is this test binary
	// running the real hook verb.
	if err := os.WriteFile(filepath.Join(configDir, singleton.FileName), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := installHookStandIn(t)
	settings := filepath.Join(t.TempDir(), "settings.json")
	if _, err := hooks.RegisterGlobal(settings, configDir, bin); err != nil {
		t.Fatal(err)
	}
	runs := []struct{ event, payload string }{
		{"SessionStart", `{"source":"startup"}`},
		{"UserPromptSubmit", `{"prompt":"hello"}`},
		{"PreToolUse", `{"tool_name":"AskUserQuestion","tool_use_id":"t1","tool_input":{"questions":[]}}`},
		{"PostToolUse", `{"tool_name":"AskUserQuestion"}`},
		{"Notification", `{"notification_type":"permission_prompt"}`},
		{"Stop", `{}`},
	}
	for n, run := range runs {
		dir := filepath.Join(work, fmt.Sprintf("run.%02d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmd"), []byte(globalCommand(t, settings, run.event)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "payload"), []byte(run.payload), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "go"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForPath(t, filepath.Join(work, "done"))
	if out, _ := os.ReadFile(filepath.Join(work, "out")); len(out) != 0 {
		t.Fatalf("the hooks printed in the pane: %s", out)
	}

	events, _, ok := m.hooks.Events(id, 0)
	if !ok {
		t.Fatal("the adopted row has no status log")
	}
	var got []string
	for _, ev := range events {
		got = append(got, ev.State+" "+ev.Name)
	}
	want := "idle SessionStart,working UserPromptSubmit,waiting PreToolUse,working PostToolUse,waiting Notification,finished Stop"
	if strings.Join(got, ",") != want {
		t.Fatalf("status log = %v, want %s", got, want)
	}
	// Let the pane go: the marker goes with it.
	m.sessions = nil
	m.syncAdoptedHooks(time.Now())
	if left, _ := os.ReadDir(m.hooks.AdoptedDir()); len(left) != 0 {
		t.Fatalf("markers left after the pane was let go: %v", left)
	}
}
