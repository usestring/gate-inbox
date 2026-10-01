// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package hooks

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/parentseal"
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
	// The ledger lookup sits beside the status writer on AskUserQuestion, and
	// the approval note beside SessionStart's; neither touches the status file.
	if post := parsed.Hooks["PostToolUse"]; len(post) != 3 || post[1].Matcher != blockingTool ||
		post[1].Hooks[0].Command != askAnsweredCommand() || !strings.Contains(askAnsweredCommand(), `hook ask-answered`) ||
		!strings.HasSuffix(askAnsweredCommand(), "exit 0") || post[2].Matcher != "*" ||
		post[2].Hooks[0].Command != attestNoteCommand() {
		t.Fatalf("PostToolUse = %+v, want the status writer, the ask-answered hook, then the attest-note hook", post)
	}
	// The attest-note hook starts no process unless an attestation is waiting.
	if cmd := attestNoteCommand(); !strings.Contains(cmd, `[ ! -f "$f`+AttestPendingSuffix+`" ] ||`) ||
		!strings.Contains(cmd, "hook attest-note") || !strings.HasSuffix(cmd, "exit 0") {
		t.Fatalf("attest-note command = %q", cmd)
	}
	if prompt := parsed.Hooks["UserPromptSubmit"]; len(prompt) != 2 ||
		prompt[1].Hooks[0].Command != promptSubmitCommand() || !strings.Contains(promptSubmitCommand(), "hook prompt-submit") {
		t.Fatalf("UserPromptSubmit = %+v, want the status writer then the prompt-submit hook", prompt)
	}
	if start := parsed.Hooks["SessionStart"]; len(start) != 2 || start[1].Hooks[0].Command != sessionStartCommand() ||
		!strings.Contains(start[1].Matcher, "compact") {
		t.Fatalf("SessionStart = %+v, want the status writer then the session-start hook", start)
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
	if command := notification.Hooks[0].Command; !strings.Contains(command, appendEvent(status.Waiting, "Notification")) || strings.Contains(command, "grep") {
		t.Fatalf("Notification command = %q, want a plain %s write", command, status.Waiting)
	}
	if got := parsed.Hooks["SessionStart"][0].Matcher; got != "startup|resume|clear" {
		t.Fatalf("SessionStart matcher = %q, want startup|resume|clear", got)
	}
	// Every API error ends the turn, and only one of them is a limit.
	if sf := parsed.Hooks["StopFailure"]; len(sf) != 1 || sf[0].Matcher != "" || sf[0].Hooks[0].Command != stopFailureCommand() {
		t.Fatalf("StopFailure = %+v, want one unfiltered type-aware command", sf)
	}
}

// Every managed session is denied the sealing keys, by its sandbox and by
// its Read and Edit tools, and the settings grant nothing: no allow rule, no
// other key.
func TestSettingsDenyTheKeyDir(t *testing.T) {
	configDir := t.TempDir()
	path, err := NewManager(configDir).EnsureSettings()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	for key := range parsed {
		if key != "hooks" && key != "permissions" && key != "sandbox" {
			t.Fatalf("settings carry %q; only hooks and the key-dir denials belong there", key)
		}
	}
	var rules struct {
		Permissions map[string][]string `json:"permissions"`
		Sandbox     struct {
			Filesystem map[string][]string `json:"filesystem"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	keyDir := parentseal.KeyDir(configDir)
	if len(rules.Permissions) != 1 || len(rules.Sandbox.Filesystem) != 1 {
		t.Fatalf("permissions %v, sandbox %v: want deny rules only", rules.Permissions, rules.Sandbox.Filesystem)
	}
	want := []string{"Read(/" + keyDir + "/**)", "Edit(/" + keyDir + "/**)"}
	if got := rules.Permissions["deny"]; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("permissions.deny = %v, want %v", got, want)
	}
	if !strings.HasPrefix(want[0], "Read(//") {
		t.Fatalf("an absolute path rule needs the // form: %s", want[0])
	}
	if got := rules.Sandbox.Filesystem["denyRead"]; len(got) != 1 || got[0] != keyDir {
		t.Fatalf("sandbox.filesystem.denyRead = %v, want [%s]", got, keyDir)
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

	writeStatus("working UserPromptSubmit\nworking PostToolUse\nfinished Stop\n")
	if got, ok := manager.Read("x"); !ok || got != status.Finished {
		t.Fatalf("Read of a log = %q, %v; want the newest event", got, ok)
	}

	// A log longer than the tail Read looks at still reads its newest line.
	writeStatus(strings.Repeat("working PostToolUse\n", 100) + "waiting Notification\n")
	if got, ok := manager.Read("x"); !ok || got != status.Waiting {
		t.Fatalf("Read of a long log = %q, %v; want waiting", got, ok)
	}

	for _, invalid := range []string{"garbage", "", "dead", "working Stop extra", "finished Stop\ngarbage\n"} {
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
			if got := strings.TrimSpace(string(raw)); got != tc.want+" PreToolUse" {
				t.Fatalf("hook wrote %q want %q", got, tc.want)
			}
		})
	}
}

// A turn that died on an API error ends there: Stop does not fire for it.
// The limit is the board's to recover; every other failure is a turn that
// ended and wants a person.
func TestStopFailureCommandEndsTheTurn(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"rate limit", `{"hook_event_name":"StopFailure","error":"rate_limit"}`, status.Errored},
		{"overloaded", `{"hook_event_name":"StopFailure","error":"overloaded"}`, status.Finished},
		{"server error", `{"hook_event_name":"StopFailure","error":"server_error"}`, status.Finished},
		{"unreadable payload", `not json`, status.Finished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "session.status")
			if err := os.WriteFile(file, []byte("working PostToolUse\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", stopFailureCommand())
			cmd.Env = append(tmuxtest.Environ(), EnvStatusFile+"="+file)
			cmd.Stdin = strings.NewReader(tc.payload)
			if err := cmd.Run(); err != nil {
				t.Fatalf("hook command failed: %v", err)
			}
			raw, _ := os.ReadFile(file)
			want := "working PostToolUse\n" + tc.want + " StopFailure\n"
			if string(raw) != want {
				t.Fatalf("log = %q want %q", raw, want)
			}
		})
	}
}

// Events is how a turn that began and ended between two polls is still
// seen: the newest line is back where it was, the lines between are not.
func TestEventsReturnsWhatHappenedSinceAnOffset(t *testing.T) {
	m := NewManager(t.TempDir())
	if _, _, ok := m.Events("s", 0); ok {
		t.Fatal("a session with no log reported events")
	}
	file := m.StatusFile("s")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	appendLog := func(s string) {
		t.Helper()
		f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(s)
		f.Close()
	}
	appendLog("finished Stop\n")
	_, cursor, _ := m.Events("s", 0)
	if cursor != int64(len("finished Stop\n")) {
		t.Fatalf("cursor %d, want the end of the log", cursor)
	}

	appendLog("working UserPromptSubmit\nworking PostToolUse\nfinished Stop\n")
	events, next, ok := m.Events("s", cursor)
	want := []Event{{status.Working, "UserPromptSubmit"}, {status.Working, "PostToolUse"}, {status.Finished, "Stop"}}
	if !ok || len(events) != len(want) {
		t.Fatalf("Events = %+v, %v; want %+v", events, ok, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %+v want %+v", i, events[i], want[i])
		}
	}

	// A line with no newline yet is still being written: not read, and
	// not skipped either.
	appendLog("waiting Noti")
	events, partial, _ := m.Events("s", next)
	if len(events) != 0 || partial != next {
		t.Fatalf("a partial line read as %+v (next %d, was %d)", events, partial, next)
	}
	appendLog("fication\n")
	if events, _, _ = m.Events("s", partial); len(events) != 1 || events[0] != (Event{status.Waiting, "Notification"}) {
		t.Fatalf("the completed line read as %+v", events)
	}

	// A log shorter than the offset was removed and begun again.
	if err := os.WriteFile(file, []byte("idle SessionStart\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if events, _, _ = m.Events("s", next); len(events) != 1 || events[0].State != status.Idle {
		t.Fatalf("a restarted log read as %+v", events)
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
	for _, state := range []string{status.Working, status.Finished} {
		cmd := exec.Command("sh", "-c", statusCommand(state, "Stop"))
		cmd.Env = append(tmuxtest.Environ(), EnvStatusFile+"="+file)
		if err := cmd.Run(); err != nil {
			t.Fatalf("hook command: %v", err)
		}
	}
	// appended, not overwritten
	if raw, err := os.ReadFile(file); err != nil || string(raw) != "working Stop\nfinished Stop\n" {
		t.Fatalf("log = %q, %v", raw, err)
	}
}

const (
	testRequest  = "00000000000000a1"
	otherRequest = "00000000000000b2"
)

// A rename file is written by agents, so a first line that is not a
// request this package issued must never reach a result path.
func TestReadNameRejectsARequestItDidNotIssue(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cases := []struct {
		label    string
		content  string
		wantName string
	}{
		{"empty request", "\nfix auth bug", "fix auth bug"},
		{"slash", "../../escape\nfix auth bug", "../../escape fix auth bug"},
		{"traversal", "..\nfix auth bug", ".. fix auth bug"},
		{"not hex", "not-a-request\nfix auth bug", "not-a-request fix auth bug"},
		{"too short", testRequest[1:] + "\nfix auth bug", testRequest[1:] + " fix auth bug"},
		{"one line", "fix auth bug", "fix auth bug"},
	}
	for _, testCase := range cases {
		t.Run(testCase.label, func(t *testing.T) {
			if err := os.WriteFile(manager.NameFile("x"), []byte(testCase.content), 0o644); err != nil {
				t.Fatalf("write name: %v", err)
			}
			name, found := manager.ReadName("x")
			if !found || name != testCase.wantName {
				t.Fatalf("ReadName = %q, %v; want the whole file as the name", name, found)
			}
			request, name, found, err := manager.ClaimName("x")
			if err != nil || !found || request != "" || name != testCase.wantName {
				t.Fatalf("ClaimName = %q, %q, %v, %v", request, name, found, err)
			}
			if err := manager.ReleaseName("x"); err != nil {
				t.Fatalf("ReleaseName: %v", err)
			}
		})
	}
}

func TestNameResultRoundTrips(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, found, err := manager.ReadNameResult("x", testRequest); found || err != nil {
		t.Fatalf("before the poller answers: found=%v err=%v", found, err)
	}

	if err := manager.WriteNameResult("x", testRequest, "fix auth  bug", "fix auth bug", nil); err != nil {
		t.Fatalf("WriteNameResult: %v", err)
	}
	verdict, found, err := manager.ReadNameResult("x", testRequest)
	if err != nil || !found || verdict.Refusal != nil || verdict.Requested != "fix auth  bug" || verdict.Applied != "fix auth bug" {
		t.Fatalf("ReadNameResult = %+v, %v, %v; want the asked and applied names", verdict, found, err)
	}

	if err := manager.WriteNameResult("x", testRequest, "taken", "", errors.New("branch already exists: am/taken")); err != nil {
		t.Fatalf("WriteNameResult refusal: %v", err)
	}
	verdict, found, err = manager.ReadNameResult("x", testRequest)
	if err != nil || !found || verdict.Refusal == nil || verdict.Refusal.Error() != "branch already exists: am/taken" || verdict.Requested != "taken" || verdict.Applied != "" {
		t.Fatalf("ReadNameResult = %+v, %v, %v; want the refusal", verdict, found, err)
	}

	if err := manager.RemoveNameResult("x", testRequest); err != nil {
		t.Fatalf("RemoveNameResult: %v", err)
	}
	if _, found, err := manager.ReadNameResult("x", testRequest); found || err != nil {
		t.Fatalf("removed result: found=%v err=%v", found, err)
	}
	if err := manager.RemoveNameResult("x", testRequest); err != nil {
		t.Fatalf("second RemoveNameResult should be a no-op: %v", err)
	}
}

// Two renames for one session are answered apart, so neither reads nor
// removes the answer meant for the other.
func TestNameResultsAreKeptApartPerRequest(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := manager.WriteNameResult("x", testRequest, "first name", "first name", nil); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := manager.WriteNameResult("x", otherRequest, "second name", "second name", nil); err != nil {
		t.Fatalf("write second: %v", err)
	}

	if err := manager.RemoveNameResult("x", otherRequest); err != nil {
		t.Fatalf("RemoveNameResult: %v", err)
	}
	verdict, found, err := manager.ReadNameResult("x", testRequest)
	if err != nil || !found || verdict.Applied != "first name" {
		t.Fatalf("the other rename took this answer with it: %+v, %v, %v", verdict, found, err)
	}

	if err := manager.SweepNameResults(time.Now().Add(NameResultLifetime)); err != nil {
		t.Fatalf("SweepNameResults: %v", err)
	}
	if _, found, _ := manager.ReadNameResult("x", testRequest); found {
		t.Fatal("an answer nobody came back for should be swept")
	}
}

// A sweep must never take an answer from a caller still reading for it,
// and must collect the ones left by sessions that are gone.
func TestSweepNameResultsKeepsFreshAnswers(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := manager.WriteNameResult("fresh", testRequest, "a name", "a name", nil); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := manager.WriteNameResult("stale", otherRequest, "another name", "another name", nil); err != nil {
		t.Fatalf("write: %v", err)
	}
	old := time.Now().Add(-NameResultLifetime - time.Minute)
	if err := os.Chtimes(manager.NameResultFile("stale", otherRequest), old, old); err != nil {
		t.Fatalf("age the answer: %v", err)
	}

	if err := manager.SweepNameResults(time.Now()); err != nil {
		t.Fatalf("SweepNameResults: %v", err)
	}
	if _, found, err := manager.ReadNameResult("fresh", testRequest); !found || err != nil {
		t.Fatalf("the sweep took an answer a caller could still be reading: found=%v err=%v", found, err)
	}
	if _, found, _ := manager.ReadNameResult("stale", otherRequest); found {
		t.Fatal("an answer past its lifetime should be gone")
	}
}

// A file this package did not write is no answer: reading one as a
// rename that happened is the false success this mailbox exists to stop.
func TestReadNameResultRejectsAForeignFile(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, content := range []string{"", "renam", "fix auth bug", "ok\nfix auth bug\nfix auth bug", "renamed\nfix auth bug", "renamed\n\nfix auth bug", "renamed\nfix auth bug\n"} {
		if err := os.WriteFile(manager.NameResultFile("x", testRequest), []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if verdict, found, err := manager.ReadNameResult("x", testRequest); found || err != nil {
			t.Fatalf("content %q read as an answer: %+v, %v", content, verdict, err)
		}
	}
}

// A mailbox that cannot be read is not the same as no answer yet: the
// waiting command must hear about it rather than report a rename queued.
func TestReadNameResultSurfacesAReadFailure(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(manager.NameResultFile("x", testRequest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, found, err := manager.ReadNameResult("x", testRequest); err == nil || found {
		t.Fatalf("unreadable result = found %v, err %v; want the failure", found, err)
	}
}

// A claim takes the pending rename out of the mailbox in one step, so a
// rename written while it is being applied waits for the next claim
// instead of being consumed with it.
func TestClaimNameLeavesALaterRenameForTheNextClaim(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, _, found, err := manager.ClaimName("x"); found || err != nil {
		t.Fatalf("ClaimName with nothing pending = %v, %v", found, err)
	}
	if err := os.WriteFile(manager.NameFile("x"), []byte(NameRequest(testRequest, "first name")), 0o644); err != nil {
		t.Fatalf("write name: %v", err)
	}

	request, name, found, err := manager.ClaimName("x")
	if err != nil || !found || name != "first name" || request != testRequest {
		t.Fatalf("ClaimName = %q, %q, %v, %v", request, name, found, err)
	}
	if _, found := manager.ReadName("x"); found {
		t.Fatal("a claimed rename must leave the mailbox free")
	}
	if err := os.WriteFile(manager.NameFile("x"), []byte(NameRequest(otherRequest, "second name")), 0o644); err != nil {
		t.Fatalf("write second name: %v", err)
	}

	// A claim the manager did not finish is picked up again ahead of it.
	request, again, found, err := manager.ClaimName("x")
	if err != nil || !found || again != "first name" || request != testRequest {
		t.Fatalf("re-claim = %q, %q, %v, %v; want the unfinished claim", request, again, found, err)
	}
	if err := manager.ReleaseName("x"); err != nil {
		t.Fatalf("ReleaseName: %v", err)
	}
	request, next, found, err := manager.ClaimName("x")
	if err != nil || !found || next != "second name" || request != otherRequest {
		t.Fatalf("next claim = %q, %q, %v, %v; want the rename written meanwhile", request, next, found, err)
	}
	if err := manager.ReleaseName("x"); err != nil {
		t.Fatalf("ReleaseName: %v", err)
	}
	if _, _, found, _ := manager.ClaimName("x"); found {
		t.Fatal("a released claim leaves nothing pending")
	}
}

// The claim tells a caller its rename is being applied, so a claim the
// manager took from a file carrying no request of ours reports no
// request rather than a made-up one.
func TestClaimedRequestReportsWhatWasClaimed(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if request, held, err := manager.ClaimedRequest("x"); held || request != "" || err != nil {
		t.Fatalf("with nothing claimed = %q, %v, %v", request, held, err)
	}

	for _, testCase := range []struct {
		label   string
		content string
		want    string
	}{
		{"a request we issued", NameRequest(testRequest, "fix auth bug"), testRequest},
		{"a file carrying no request", "fix auth bug", ""},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			if err := os.WriteFile(manager.NameFile("x"), []byte(testCase.content), 0o644); err != nil {
				t.Fatalf("write name: %v", err)
			}
			if _, _, found, err := manager.ClaimName("x"); err != nil || !found {
				t.Fatalf("ClaimName = %v, %v", found, err)
			}
			request, held, err := manager.ClaimedRequest("x")
			if err != nil || !held || request != testCase.want {
				t.Fatalf("ClaimedRequest = %q, %v, %v; want %q", request, held, err, testCase.want)
			}
			if err := manager.ReleaseName("x"); err != nil {
				t.Fatalf("ReleaseName: %v", err)
			}
		})
	}
}

// Two renames for one session write at the same time, so neither may
// publish the other's content or fail on a staging file it does not own.
func TestWriteWholeSurvivesConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abc123.name")
	var wg sync.WaitGroup
	for _, content := range []string{"first name", "second name"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if err := WriteWhole(path, content); err != nil {
					t.Errorf("WriteWhole(%q): %v", content, err)
					return
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				if got := string(raw); got != "first name" && got != "second name" {
					t.Errorf("read a torn file: %q", got)
					return
				}
			}
		}()
	}
	wg.Wait()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("staging files were left behind: %v", entries)
	}
}

// A queued rename leads with its request id; ReadName hands back only the
// name, so a caller reading the mailbox never mistakes the id for part of it.
func TestReadNameDropsTheRequestLine(t *testing.T) {
	manager := NewManager(t.TempDir())
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(manager.NameFile("x"), []byte(NameRequest(testRequest, "fix auth bug")), 0o644); err != nil {
		t.Fatalf("write name: %v", err)
	}
	if name, found := manager.ReadName("x"); !found || name != "fix auth bug" {
		t.Fatalf("ReadName = %q, %v; want the name alone", name, found)
	}
}
