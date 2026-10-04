package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// A claude the board adopted has no GATE_INBOX_SESSION_ID, so its shell
// commands find their row through the pane's adoption marker; a launched
// session's environment still wins.
func TestCallerIDFallsBackToTheAdoptionMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(hooks.EnvSessionID, "")
	t.Setenv("TMUX", "/run/tmux-test/default,4242,0")
	t.Setenv("TMUX_PANE", "%7")
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID with no marker = %q, want none", got)
	}
	if err := hooks.NewManager(dir).SyncAdopted([]hooks.AdoptedPane{
		{ID: "a1b2c3d4", ServerPID: 4242, PaneID: "%7", AgentPID: os.Getppid()},
	}); err != nil {
		t.Fatal(err)
	}
	if got := callerID(dir); got != "a1b2c3d4" {
		t.Fatalf("callerID in an adopted pane = %q, want a1b2c3d4", got)
	}
	t.Setenv(hooks.EnvSessionID, "launched")
	if got := callerID(dir); got != "launched" {
		t.Fatalf("callerID with a launch's id = %q, want launched", got)
	}
	t.Setenv(hooks.EnvSessionID, "")
	t.Setenv("TMUX_PANE", "")
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID outside tmux = %q, want none", got)
	}
}

// callerIDChildEnv runs TestCallerIDFromUnderAnAdoptedCodex's child half:
// the test binary under a stand-in codex, printing the row it speaks as.
const callerIDChildEnv = "GATE_INBOX_TEST_CALLERID_DIR"

// A codex the board adopted has no launch environment either; a command its
// shell tool runs, a few processes under it, finds the row through the
// marker naming the process adoption identified as the codex.
func TestCallerIDFromUnderAnAdoptedCodex(t *testing.T) {
	if dir := os.Getenv(callerIDChildEnv); dir != "" {
		// The test binary drops $TMUX and $TMUX_PANE as it starts, so the
		// pane's arrive under other names.
		t.Setenv("TMUX", os.Getenv(callerIDChildEnv+"_TMUX"))
		t.Setenv("TMUX_PANE", os.Getenv(callerIDChildEnv+"_PANE"))
		fmt.Print("caller=" + callerID(dir) + "\n")
		return
	}
	dir, work := t.TempDir(), t.TempDir()
	codex := filepath.Join(work, "codex")
	// Waits to be told the marker is there, then runs what it was given
	// through a shell, the way its shell tool would. It never execs away,
	// so it stays in the process tree under its own name.
	script := "#!/bin/sh\necho $$ > '" + filepath.Join(work, "pid") + "'\n" +
		"while [ ! -f '" + filepath.Join(work, "go") + "' ]; do sleep 0.05; done\n" +
		"sh -c '\"$0\" \"$@\"; exit' \"$@\"\n"
	if err := os.WriteFile(codex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(codex, os.Args[0], "-test.run=^TestCallerIDFromUnderAnAdoptedCodex$", "-test.count=1")
	cmd.Env = append(tmuxtest.Environ(),
		callerIDChildEnv+"="+dir,
		hooks.EnvSessionID+"=",
		callerIDChildEnv+"_TMUX="+tmuxtest.SocketPath("callerid")+",4242,0",
		callerIDChildEnv+"_PANE=%7",
	)
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(work, "pid")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	agent, ok := adopt.NewProcTable().ProgramPID(int32(cmd.Process.Pid), "codex")
	if !ok || agent != cmd.Process.Pid {
		t.Fatalf("ProgramPID = %d, %v; want the stand-in codex, %d", agent, ok, cmd.Process.Pid)
	}
	if err := hooks.NewManager(dir).SyncAdopted([]hooks.AdoptedPane{
		{ID: "codexrow", ServerPID: 4242, PaneID: "%7", AgentPID: agent, Tool: "codex"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "caller=codexrow\n") {
		t.Fatalf("callerID under the adopted codex: %q, want codexrow", out.String())
	}
}
