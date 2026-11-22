package closecmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

func testConfig(command string) config.Config {
	return config.Config{
		CloseCommand: command,
		Tools: map[string]config.Tool{
			"claude": {Command: "claude"},
			"shell":  {Command: "sh", Shell: true},
		},
	}
}

// waitForFile waits for the detached command to write path.
func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 && strings.HasSuffix(string(data), "\ndone\n") {
			return data
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the close command never wrote %s", path)
	return nil
}

// The command runs in the closed session's directory, reads a
// SessionEnd-shaped payload naming the agent's conversation, and does not
// inherit the closing process's own session identity.
func TestClosedRunsTheCommandForTheClosedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.HomeEnv, home)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "closer-conversation")
	t.Setenv("TMUX_PANE", "%9")
	cwd := t.TempDir()
	out := filepath.Join(t.TempDir(), "out")

	command := `{ cat; echo; pwd -P; echo "claude=${CLAUDE_CODE_SESSION_ID-unset} pane=${TMUX_PANE-unset} closed=$GATE_INBOX_CLOSED_SESSION_ID agent=$GATE_INBOX_CLOSED_AGENT_SESSION_ID reason=$GATE_INBOX_CLOSE_REASON"; echo done; } > ` + out
	Closed(testConfig(command), ReasonKilled, store.Session{ID: "row00001", Tool: "claude", AgentSessionID: "agent-conversation", Cwd: cwd})

	lines := strings.Split(strings.TrimSuffix(string(waitForFile(t, out)), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("output = %q", lines)
	}
	var payload Payload
	if err := json.Unmarshal([]byte(lines[0]), &payload); err != nil {
		t.Fatalf("payload %q: %v", lines[0], err)
	}
	want := Payload{SessionID: "agent-conversation", Cwd: cwd, HookEventName: "SessionEnd", Reason: ReasonKilled, GateInboxSessionID: "row00001", Tool: "claude"}
	if payload != want {
		t.Errorf("payload = %+v, want %+v", payload, want)
	}
	if resolved, _ := filepath.EvalSymlinks(cwd); lines[1] != resolved {
		t.Errorf("ran in %q, want %q", lines[1], resolved)
	}
	if want := "claude=unset pane=unset closed=row00001 agent=agent-conversation reason=" + ReasonKilled; lines[2] != want {
		t.Errorf("env = %q, want %q", lines[2], want)
	}
	logged, err := os.ReadFile(filepath.Join(home, "logs", LogName))
	if err != nil || !strings.Contains(string(logged), "close command for row00001") {
		t.Errorf("log = %q, %v; want the run recorded", logged, err)
	}
}

// Nothing starts without a command, for a terminal, for a session with no
// conversation id to name, or for one whose directory is gone.
func TestClosedSkipsWhatItCannotRunFor(t *testing.T) {
	t.Setenv(config.HomeEnv, t.TempDir())
	var started []string
	previous := start
	start = func(cmd *exec.Cmd) error {
		started = append(started, cmd.Dir)
		return nil
	}
	t.Cleanup(func() { start = previous })
	cwd := t.TempDir()
	ok := store.Session{ID: "row00001", Tool: "claude", AgentSessionID: "a", Cwd: cwd}

	Closed(testConfig(""), ReasonKilled, ok)
	Closed(testConfig("true"), ReasonKilled,
		store.Session{ID: "term0001", Tool: "shell", AgentSessionID: "a", Cwd: cwd},
		store.Session{ID: "noid0001", Tool: "claude", Cwd: cwd},
		store.Session{ID: "gone0001", Tool: "claude", AgentSessionID: "a", Cwd: filepath.Join(cwd, "missing")},
	)
	if len(started) != 0 {
		t.Fatalf("started in %v, want nothing", started)
	}
	Closed(testConfig("true"), ReasonArchived, ok, ok)
	if len(started) != 2 || started[0] != cwd {
		t.Fatalf("started = %v, want one run per session in %s", started, cwd)
	}
}
