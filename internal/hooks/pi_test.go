package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiStatusMapsTheLifecycle(t *testing.T) {
	cases := []struct {
		name                 string
		p                    PiPayload
		lastState, lastEvent string
		state                string
		remove               bool
	}{
		{"start", PiPayload{Event: "session_start", Reason: "startup"}, "", "", "idle", false},
		{"resume", PiPayload{Event: "session_start", Reason: "resume"}, "", "", "idle", false},
		{"run", PiPayload{Event: "agent_start"}, "idle", "session_start", "working", false},
		{"run ends", PiPayload{Event: "agent_end"}, "working", "agent_start", "", false},
		{"run fails", PiPayload{Event: "agent_end", Errored: true}, "working", "agent_start", "errored", false},
		{"settled", PiPayload{Event: "agent_settled"}, "working", "agent_start", "finished", false},
		{"settled after error", PiPayload{Event: "agent_settled"}, "errored", "agent_end", "", false},
		{"dialog", PiPayload{Event: "ui_prompt_start"}, "working", "agent_start", "waiting", false},
		{"dialog closes mid-run", PiPayload{Event: "ui_prompt_end"}, "waiting", "ui_prompt_start", "working", false},
		{"dialog closes at rest", PiPayload{Event: "ui_prompt_end", Idle: true}, "waiting", "ui_prompt_start", "idle", false},
		{"quit", PiPayload{Event: "session_shutdown", Reason: "quit"}, "finished", "agent_settled", "", true},
		{"replaced", PiPayload{Event: "session_shutdown", Reason: "new"}, "finished", "agent_settled", "", false},
		{"unknown", PiPayload{Event: "turn_start"}, "", "", "", false},
	}
	for _, c := range cases {
		state, remove := PiStatus(c.p, c.lastState, c.lastEvent)
		if state != c.state || remove != c.remove {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, state, remove, c.state, c.remove)
		}
	}
}

// One turn the way the extension reports it: the file logs idle, working,
// waiting while a dialog stands, working again, finished, and goes on quit.
func TestDispatchPiWritesTheStatusFile(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)
	dir := m.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := m.StatusFile("abcd1234")
	for _, step := range []struct{ event, payload string }{
		{"session_start", `{"event":"session_start","reason":"startup"}`},
		{"agent_start", `{"event":"agent_start"}`},
		{"ui_prompt_start", `{"event":"ui_prompt_start","kind":"select"}`},
		{"ui_prompt_end", `{"event":"ui_prompt_end","idle":false}`},
		{"agent_end", `{"event":"agent_end","errored":false}`},
		{"agent_settled", `{"event":"agent_settled","aborted":false}`},
	} {
		if out := DispatchPi(step.event, strings.NewReader(step.payload), file); out != "" {
			t.Fatalf("%s answered %q", step.event, out)
		}
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "idle session_start\nworking agent_start\nwaiting ui_prompt_start\nworking ui_prompt_end\nfinished agent_settled\n"
	if string(raw) != want {
		t.Fatalf("status file:\n%s\nwant:\n%s", raw, want)
	}
	if got, ok := m.Read("abcd1234"); !ok || got != "finished" {
		t.Fatalf("the poller's reader sees %q, %v", got, ok)
	}
	for _, sibling := range []string{AttestPendingSuffix, SandboxStaleSuffix} {
		if err := os.WriteFile(file+sibling, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	DispatchPi("session_shutdown", strings.NewReader(`{"event":"session_shutdown","reason":"quit","cwd":"`+dir+`"}`), file)
	for _, path := range []string{file, file + AttestPendingSuffix, file + SandboxStaleSuffix} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived quit: %v", path, err)
		}
	}
}

// An error stands through the settle that follows it, and a retry that
// starts again says working.
func TestDispatchPiKeepsAnErrorStanding(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.status")
	// A long session's log, so the last line is read from the tail.
	history := strings.Repeat("working agent_start\nfinished agent_settled\n", 200)
	if err := os.WriteFile(file, []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	DispatchPi("agent_start", strings.NewReader(`{}`), file)
	DispatchPi("agent_end", strings.NewReader(`{"errored":true}`), file)
	DispatchPi("agent_settled", strings.NewReader(`{}`), file)
	raw, _ := os.ReadFile(file)
	if string(raw) != history+"working agent_start\nerrored agent_end\n" {
		t.Fatalf("status file ends:\n%s", raw[len(raw)-80:])
	}
}

// Outside a board session, or for a payload that names another event, the
// hook writes nothing.
func TestDispatchPiRefusesWhatItCannotPlace(t *testing.T) {
	if out := DispatchPi("agent_start", strings.NewReader(`{}`), ""); out != "" {
		t.Fatalf("answered %q with no status file", out)
	}
	file := filepath.Join(t.TempDir(), "s.status")
	DispatchPi("agent_start", strings.NewReader(`{"event":"session_shutdown"}`), file)
	DispatchPi("agent_start", strings.NewReader(`not json`), file)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("a refused payload wrote the file: %v", err)
	}
}

func TestFileStatusCoversBothHookSources(t *testing.T) {
	for source, want := range map[string]bool{StatusSourceClaude: true, StatusSourcePi: true, "": false, "pane": false} {
		if FileStatus(source) != want {
			t.Errorf("FileStatus(%q) = %v", source, !want)
		}
	}
}
