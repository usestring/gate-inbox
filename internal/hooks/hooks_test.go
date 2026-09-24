// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestEnsureSettingsWritesValidHookJSON(t *testing.T) {
	manager := NewManager(t.TempDir())
	path, err := manager.EnsureSettings()
	if err != nil {
		t.Fatalf("EnsureSettings: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var parsed struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("settings is not valid JSON: %v", err)
	}
	events := []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "StopFailure", "SessionStart", "SessionEnd"}
	if len(parsed.Hooks) != len(events) {
		t.Fatalf("hooks has %d events, want %d: %v", len(parsed.Hooks), len(events), parsed.Hooks)
	}
	guard := statusFileVar + `[ -z "$f" ] ||`
	for _, event := range events {
		matchers, ok := parsed.Hooks[event]
		if !ok {
			t.Fatalf("event %s missing from settings", event)
		}
		for _, matcher := range matchers {
			for _, hook := range matcher.Hooks {
				if hook.Type != "command" {
					t.Fatalf("event %s hook type = %q, want command", event, hook.Type)
				}
				if !strings.Contains(hook.Command, guard) {
					t.Fatalf("event %s command lacks env guard: %q", event, hook.Command)
				}
			}
		}
	}
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		if got := parsed.Hooks[event][0].Matcher; got != "*" {
			t.Fatalf("%s matcher = %q, want *", event, got)
		}
	}
	// PreToolUse carries the one command that tells AskUserQuestion apart
	// from ordinary work. Two matchers under one event would run in parallel
	// and race on the file, and matchers cannot exclude a tool, so the
	// single "*" entry has to be the branching one.
	if pre := parsed.Hooks["PreToolUse"]; len(pre) != 1 || pre[0].Hooks[0].Command != preToolUseCommand() {
		t.Fatalf("PreToolUse = %+v, want the single tool-aware command", pre)
	}
	notification := parsed.Hooks["Notification"][0]
	if notification.Matcher != blockingNotifications {
		t.Fatalf("Notification matcher = %q, want %q", notification.Matcher, blockingNotifications)
	}
	// the idle reminder, auth success and background agents finishing all
	// arrive as Notification too, and the matcher is what keeps them out
	for _, unwanted := range []string{"idle_prompt", "auth_success", "agent_completed"} {
		if strings.Contains(notification.Matcher, unwanted) {
			t.Fatalf("Notification matcher %q subscribes to %s", notification.Matcher, unwanted)
		}
	}
	if command := notification.Hooks[0].Command; !strings.Contains(command, "printf "+status.Waiting) || strings.Contains(command, "grep") {
		t.Fatalf("Notification command = %q, want a plain %s write", command, status.Waiting)
	}
	if got := parsed.Hooks["SessionStart"][0].Matcher; got != "startup|resume|clear" {
		t.Fatalf("SessionStart matcher = %q, want startup|resume|clear", got)
	}
	stopFailure := parsed.Hooks["StopFailure"][0]
	if stopFailure.Matcher != limitStopFailures {
		t.Fatalf("StopFailure matcher = %q, want %q", stopFailure.Matcher, limitStopFailures)
	}
	if command := stopFailure.Hooks[0].Command; !strings.Contains(command, "printf "+status.Errored) {
		t.Fatalf("StopFailure command = %q, want a plain %s write", command, status.Errored)
	}
}

func TestEnsureSettingsIdempotent(t *testing.T) {
	manager := NewManager(t.TempDir())
	first, err := manager.EnsureSettings()
	if err != nil {
		t.Fatalf("first EnsureSettings: %v", err)
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	second, err := manager.EnsureSettings()
	if err != nil {
		t.Fatalf("second EnsureSettings: %v", err)
	}
	if first != second {
		t.Fatalf("paths differ: %q vs %q", first, second)
	}
	again, err := os.Stat(second)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.ModTime().Equal(again.ModTime()) {
		t.Fatal("unchanged settings should not be rewritten")
	}
}

func TestStatusFilePath(t *testing.T) {
	configDir := t.TempDir()
	manager := NewManager(configDir)
	want := filepath.Join(configDir, "hooks", "abcd1234.status")
	if got := manager.StatusFile("abcd1234"); got != want {
		t.Fatalf("StatusFile = %q, want %q", got, want)
	}
}

func TestReadWhitelist(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(manager.StatusFile("x")), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeStatus := func(content string) {
		t.Helper()
		if err := os.WriteFile(manager.StatusFile("x"), []byte(content), 0o644); err != nil {
			t.Fatalf("write status: %v", err)
		}
	}

	for _, valid := range []string{status.Working, status.Waiting, status.Finished, status.Idle, status.Errored} {
		writeStatus(valid)
		got, ok := manager.Read("x")
		if !ok || got != valid {
			t.Fatalf("Read(%q) = %q, %v; want value, true", valid, got, ok)
		}
	}

	writeStatus("working\n")
	if got, ok := manager.Read("x"); !ok || got != status.Working {
		t.Fatalf("trailing newline should trim to working, got %q, %v", got, ok)
	}

	for _, invalid := range []string{"garbage", "", "dead"} {
		writeStatus(invalid)
		if got, ok := manager.Read("x"); ok {
			t.Fatalf("Read(%q) accepted %q, want rejection", invalid, got)
		}
	}

	if _, ok := manager.Read("no-such-session"); ok {
		t.Fatal("missing file should not read ok")
	}
}

func TestReadNameNormalizes(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(manager.NameFile("x")), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeName := func(content string) {
		t.Helper()
		if err := os.WriteFile(manager.NameFile("x"), []byte(content), 0o644); err != nil {
			t.Fatalf("write name: %v", err)
		}
	}

	writeName("  fix   auth\nbug \n")
	if got, found := manager.ReadName("x"); !found || got != "fix auth bug" {
		t.Fatalf("ReadName = %q, %v; want squashed line, true", got, found)
	}

	writeName("   \n\t")
	if got, found := manager.ReadName("x"); !found || got != "" {
		t.Fatalf("whitespace file: got %q, %v; want empty name with found=true", got, found)
	}

	writeName(strings.Repeat("é", maxNameLength+20))
	got, _ := manager.ReadName("x")
	if runes := []rune(got); len(runes) != maxNameLength {
		t.Fatalf("long name should cap at %d runes, got %d", maxNameLength, len(runes))
	}

	if _, found := manager.ReadName("no-such-session"); found {
		t.Fatal("missing name file should not be found")
	}

	if err := manager.RemoveName("x"); err != nil {
		t.Fatalf("RemoveName: %v", err)
	}
	if err := manager.RemoveName("x"); err != nil {
		t.Fatalf("second RemoveName should be a no-op: %v", err)
	}
	if _, found := manager.ReadName("x"); found {
		t.Fatal("removed name file should not be found")
	}
}

func TestRemoveIdempotent(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(manager.StatusFile("x")), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(manager.StatusFile("x"), []byte(status.Working), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := manager.Remove("x"); err != nil {
		t.Fatalf("first Remove: %v", err)
	}
	if err := manager.Remove("x"); err != nil {
		t.Fatalf("second Remove should be a no-op: %v", err)
	}
}

// The PreToolUse command is the only hook that fires for AskUserQuestion,
// and nothing else fires until a person answers, so what it writes is the
// status the session shows for as long as the dialog is up. Run the real
// shell it generates rather than asserting on its text.
func TestPreToolUseCommandReportsTheQuestionDialog(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"question dialog", `{"session_id":"x","tool_name":"AskUserQuestion","tool_input":{}}`, status.Waiting},
		{"spaced payload", `{ "tool_name" : "AskUserQuestion" }`, status.Waiting},
		{"ordinary tool", `{"session_id":"x","tool_name":"Bash","tool_input":{"command":"ls"}}`, status.Working},
		{"tool merely naming it", `{"tool_name":"Bash","tool_input":{"command":"grep AskUserQuestion ."}}`, status.Working},
		{"unreadable payload", `not json`, status.Working},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "session.status")
			cmd := exec.Command("sh", "-c", preToolUseCommand())
			cmd.Env = append(tmuxtest.Environ(), EnvStatusFile+"="+file)
			cmd.Stdin = strings.NewReader(tc.payload)
			if err := cmd.Run(); err != nil {
				t.Fatalf("hook command failed: %v", err)
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("hook wrote no status: %v", err)
			}
			if got := strings.TrimSpace(string(raw)); got != tc.want {
				t.Fatalf("hook wrote %q want %q", got, tc.want)
			}
		})
	}
}

// Outside a managed session the hook must stay a no-op, as every other
// one does.
func TestPreToolUseCommandNoOpsWithoutAStatusFile(t *testing.T) {
	cmd := exec.Command("sh", "-c", preToolUseCommand())
	cmd.Env = append(tmuxtest.Environ(), EnvStatusFile+"=")
	cmd.Stdin = strings.NewReader(`{"tool_name":"AskUserQuestion"}`)
	if err := cmd.Run(); err != nil {
		t.Fatalf("hook command failed outside a managed session: %v", err)
	}
}

// Write has to accept exactly the states Read accepts and nothing else.
func TestWriteRoundTripsAndRefusesANonStatus(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "config"))
	if err := m.Write("s1", status.Waiting); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, ok := m.Read("s1"); !ok || got != status.Waiting {
		t.Fatalf("Read = %q (%v), want waiting", got, ok)
	}
	if err := m.Write("s1", status.Finished); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, _ := m.Read("s1"); got != status.Finished {
		t.Fatalf("Read = %q, want finished", got)
	}
	if err := m.Write("s1", "escalated"); err == nil {
		t.Fatal("a word that is not a status was written")
	}
	if got, _ := m.Read("s1"); got != status.Finished {
		t.Fatalf("a refused write changed the file to %q", got)
	}
}

// TestSessionEndPrunesWorktrees pins the teardown prune: without it stale
// worktree records accumulate until the sandbox profile exceeds the
// kernel argv limit and every sandboxed command fails with E2BIG.
func TestSessionEndPrunesWorktrees(t *testing.T) {
	cmd := sessionEndCommand()
	if !strings.HasPrefix(cmd, statusFileVar+`[ -z "$f" ] ||`) {
		t.Fatalf("session end command must keep the status-file guard first: %q", cmd)
	}
	if !strings.Contains(cmd, "rm -f") {
		t.Fatalf("session end command no longer removes the status file: %q", cmd)
	}
	if !strings.Contains(cmd, "git worktree prune") {
		t.Fatalf("session end command does not prune worktrees: %q", cmd)
	}
	if !strings.Contains(cmd, "git submodule --quiet foreach --recursive") {
		t.Fatalf("session end command does not prune submodule worktrees: %q", cmd)
	}
	// Teardown must not fail on a repository that moved, and a session
	// ending outside a git tree is normal.
	if !strings.HasSuffix(cmd, "exit 0") {
		t.Fatalf("session end command must always succeed: %q", cmd)
	}
}

// The hooks find the status file through the variable the launch exports.
func TestStatusCommandWritesTheExportedStatusFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "status")
	cmd := exec.Command("sh", "-c", statusCommand(status.Working))
	cmd.Env = append(tmuxtest.Environ(), EnvStatusFile+"="+file)
	if err := cmd.Run(); err != nil {
		t.Fatalf("hook command: %v", err)
	}
	if raw, err := os.ReadFile(file); err != nil || strings.TrimSpace(string(raw)) != status.Working {
		t.Fatalf("status = %q, %v; want %s", raw, err, status.Working)
	}
}
