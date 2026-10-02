package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

const (
	judgeStarted  = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_judge","type":"tool_result","content":"Command running in background with ID: bjudge1."}]},"toolUseResult":{"stdout":"","backgroundTaskId":"bjudge1"}}`
	judgeNotified = `{"type":"queue-operation","operation":"enqueue","content":"<task-notification>\n<task-id>bjudge1</task-id>\n<status>completed</status>\n</task-notification>"}`
)

// claudeTranscript points HOME at a scratch directory and returns where
// Claude Code would keep sess's transcript under it.
func claudeTranscript(t *testing.T, sess store.Session) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path, err := agentsession.ClaudeTranscriptPath(sess.Cwd, sess.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// The 2026-10-02 loss, replayed: a child ends its turn with two judge runs
// going in the background, the board tells its spawner it finished, the
// spawner reads that and leaves it alone past the grace -- and the sweep
// ended the agent and the runs with it. A transcript still naming a task
// that has not ended holds the child; the notice that the task ended frees
// it to go as before.
func TestTheChildSweepHoldsAFinishedChildWithBackgroundWorkRunning(t *testing.T) {
	m, parent, kid := finishedFanOut(t)
	if err := m.store.SetAgentSessionID(kid.ID, "conv-judges"); err != nil {
		t.Fatalf("SetAgentSessionID: %v", err)
	}
	loadStoredRows(t, m)
	kid, _ = m.sessionByID(kid.ID)
	transcript := claudeTranscript(t, kid)
	appendTranscript(t, transcript, judgeStarted)
	relayRest(t, m, parent, kid, true)

	runChildSweep(t, m)
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived || !m.tmux.Exists(kid.ID) {
		t.Fatalf("the sweep ended a child whose background shell was still running: %+v, %v", row, err)
	}

	appendTranscript(t, transcript, judgeNotified)
	loadStoredRows(t, m)
	runChildSweep(t, m)
	row, err := m.store.Get(kid.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !row.Archived || m.tmux.Exists(kid.ID) {
		t.Fatalf("the sweep kept a finished child after its background work ended: %+v", row)
	}
}

// The status the spawner is told follows the transcript too: Stop fired, but
// a task still pending keeps the row working, however the pane is drawn.
func TestHookFinishedReadsWorkingWhileTheTranscriptNamesAPendingTask(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked-bg", Tool: "claude-hooked", Cwd: t.TempDir(), AgentSessionID: "conv-bg"}
	transcript := claudeTranscript(t, sess)
	appendTranscript(t, transcript, judgeStarted)
	writeHookStatus(t, m, sess.ID, status.Finished)

	pane := "⏺ The judges are still running.\n✻ Worked for 3s\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("a pending background task should keep hook finished working, got %q", got)
	}
	sess.Acked = true
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("an acked session with a pending background task should still read working, got %q", got)
	}

	appendTranscript(t, transcript, judgeNotified)
	sess.Acked = false
	if got := deriveStatus(t, m, sess, pane, true); got != status.Finished {
		t.Fatalf("once the task ended the hook's finish should stand, got %q", got)
	}
}

// The pane fallback: a summary Claude wrapped in a narrow pane still names
// the shells left running, so Stop's finish is upgraded to working.
func TestHookFinishedUpgradesToWorkingOnAWrappedShellsTail(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked-wrap", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Finished)

	pane := "⏺ The judges are still running; the final report\n  goes out once they finish.\n\n✻ Sautéed for 8m 35s · done 0:41 · 2 shells still \n  running\n\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("a wrapped still-running tail should upgrade hook finished to working, got %q", got)
	}
}

// The process tree is the last word: a Bash tool shell still up under the
// agent holds it whatever the transcript and the pane say.
func TestChildBackgroundWorkCountsBashToolShellsUnderThePane(t *testing.T) {
	shell := exec.Command("sh", "-c", "sleep 30; : /opt/agent/shell-snapshots/snapshot-zsh-1.sh")
	shell.Env = tmuxtest.Environ()
	if err := shell.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = shell.Process.Kill()
		_ = shell.Wait()
	})
	self := func(string) (int, error) { return os.Getpid(), nil }
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, shells := childBackgroundWork(nil, store.Session{ID: "kid"}, self)
		if shells == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("shells = %d, want the one marked shell under this process", shells)
		}
		time.Sleep(50 * time.Millisecond)
	}

	_ = shell.Process.Kill()
	_ = shell.Wait()
	if _, shells := childBackgroundWork(nil, store.Session{ID: "kid"}, self); shells != 0 {
		t.Fatalf("shells = %d after the shell exited", shells)
	}
}

// Only the outermost of a sandboxed chain counts, and nothing outside the
// pane's tree does.
func TestClaudeShellsUnderCountsEachShellOnce(t *testing.T) {
	table := func() ([]byte, error) {
		return []byte("" +
			"  10     1 claude\n" +
			"  11    10 gate-inbox mcp\n" +
			"  12    10 bwrap --new-session -- /usr/bin/zsh -c source /opt/agent/shell-snapshots/snapshot-zsh-1.sh && eval 'sleep 600'\n" +
			"  13    12 /usr/bin/zsh -c source /opt/agent/shell-snapshots/snapshot-zsh-1.sh && eval 'sleep 600'\n" +
			"  14    10 /usr/bin/zsh -c source /opt/agent/shell-snapshots/snapshot-zsh-1.sh && eval 'judge run'\n" +
			"  20     1 /usr/bin/zsh -c source /opt/agent/shell-snapshots/snapshot-zsh-1.sh && eval 'elsewhere'\n"), nil
	}
	if got := claudeShellsUnder(10, table); got != 2 {
		t.Fatalf("claudeShellsUnder = %d, want 2", got)
	}
	if got := claudeShellsUnder(11, table); got != 0 {
		t.Fatalf("claudeShellsUnder(mcp) = %d, want 0", got)
	}
}
