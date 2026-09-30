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

const userConfigMarker = "@gitest-user-config"

// withUserConfig points HOME and XDG_CONFIG_HOME at a directory whose tmux
// config sets userConfigMarker, in both places tmux looks for one.
func withUserConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	if err := os.MkdirAll(filepath.Join(xdg, "tmux"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := []byte("set -g " + userConfigMarker + " loaded\n")
	for _, path := range []string{filepath.Join(home, ".tmux.conf"), filepath.Join(xdg, "tmux", "tmux.conf")} {
		if err := os.WriteFile(path, line, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
}

// markerOn starts a server with bin on a fresh test socket and reads the
// marker back from it.
func markerOn(t *testing.T, bin string) string {
	t.Helper()
	socket := Socket(t, "config")
	Check(t, socket)
	start := exec.Command(bin, "-L", socket, "new-session", "-d", "sleep 60")
	start.Env = Environ()
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	show := exec.Command(bin, "-L", socket, "show-options", "-gqv", userConfigMarker)
	show.Env = Environ()
	out, err := show.Output()
	if err != nil {
		t.Fatalf("show-options: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestTestServersIgnoreTheUserConfig(t *testing.T) {
	found, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	if found != ShimPath() {
		t.Fatalf("tmux resolves to %s, want this run's shim %s", found, ShimPath())
	}
	withUserConfig(t)
	if got := markerOn(t, "tmux"); got != "" {
		t.Fatalf("a test server sourced the user's tmux config: %s = %q", userConfigMarker, got)
	}
}

// Without the shim the same server does read the config, so the test above
// measures the shim and not a HOME tmux never looked at.
func TestTheUnwrappedTmuxWouldSourceIt(t *testing.T) {
	shim := ShimPath()
	if shim == "" {
		t.Skip("tmux not installed")
	}
	script, err := os.ReadFile(shim)
	if err != nil {
		t.Fatal(err)
	}
	_, rest, _ := strings.Cut(string(script), "exec '")
	real, _, ok := strings.Cut(rest, "'")
	if !ok {
		t.Fatalf("cannot read the real tmux out of the shim:\n%s", script)
	}
	withUserConfig(t)
	if got := markerOn(t, real); got != "loaded" {
		t.Fatalf("the unwrapped tmux did not source the user's config (%s = %q): the test above proves nothing", userConfigMarker, got)
	}
}
