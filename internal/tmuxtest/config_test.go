package tmuxtest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// serverPID starts a server on socket through whatever tmux PATH finds -- the
// same lookup the product driver and raw test commands make -- and returns its
// pid.
func serverPID(t *testing.T, env []string, args ...string) int {
	t.Helper()
	start := exec.Command("tmux", append(args, "new-session", "-d", "sleep 60")...)
	start.Env = env
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	query := exec.Command("tmux", append(args, "display-message", "-p", "#{pid}")...)
	query.Env = env
	out, err := query.Output()
	if err != nil {
		t.Fatalf("display-message: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("server pid %q: %v", out, err)
	}
	return pid
}

func TestTestServersSkipTheOperatorsConfig(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := Socket(t, "config")
	Check(t, socket)
	pid := serverPID(t, Environ(), "-L", socket)
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		t.Skipf("no /proc to read the server's argv from: %v", err)
	}
	// The server keeps the argv of the client that forked it.
	if !bytes.Contains(cmdline, []byte("\x00-f\x00/dev/null\x00")) {
		t.Fatalf("test server started without -f /dev/null, so it loads ~/.tmux.conf: %q", cmdline)
	}
}

func TestSocketKillsItsServerWhenTheTestEnds(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	var socket string
	t.Run("fixture", func(t *testing.T) {
		socket = Socket(t, "cleanup")
		serverPID(t, Environ(), "-L", socket)
	})
	if ServerAlive(socket) {
		t.Fatalf("server on %s outlived the test that started it", socket)
	}
}

// orphanedRun lays out a private directory the way isolate does, with a server
// in it, and returns the directory and the server's pid. locked says whether
// its owner is still alive.
func orphanedRun(t *testing.T, locked bool) (string, int) {
	t.Helper()
	dir, err := os.MkdirTemp(tempRoot(), "gitmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	lock, err := os.Create(filepath.Join(dir, ownerFile))
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { lock.Close() })
	} else {
		lock.Close()
	}
	socket := filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()), NewSocket("orphan"))
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	return dir, serverPID(t, ScrubEnv(os.Environ()), "-S", socket)
}

// alive waits out a server that was told to exit a moment ago.
func alive(pid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestReapCollectsOnlyARunWhoseOwnerIsGone(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	deadDir, deadPID := orphanedRun(t, false)
	liveDir, livePID := orphanedRun(t, true)

	reapOrphanedRuns()

	if alive(deadPID) {
		t.Errorf("server %d of a run with no owner survived the reap", deadPID)
	}
	if _, err := os.Stat(deadDir); !os.IsNotExist(err) {
		t.Errorf("directory %s of a run with no owner survived the reap: %v", deadDir, err)
	}
	if syscall.Kill(livePID, 0) != nil {
		t.Errorf("server %d of a run whose owner holds its lock was killed", livePID)
	}
	if _, err := os.Stat(liveDir); err != nil {
		t.Errorf("directory %s of a live run was removed: %v", liveDir, err)
	}
}
