package tmuxtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxguard"
)

// Isolation is a property of the test binary, not a habit: importing this
// package isolates the process before any TestMain or test runs.
//
// A test process inherits TMUX from the pane `go test` was typed into, and
// tmux honours TMUX ahead of TMUX_TMPDIR. A bare `tmux kill-server` in a
// test cleanup would take down the caller's live server, other tests would
// create and kill sessions there, and a real board would run against it
// through children that inherit the environment. So before anything else
// runs:
//
//   - TMUX and TMUX_PANE are unset, so nothing can follow them to a live pane;
//   - TMUX_TMPDIR is a fresh directory of this run's own, so every -L socket
//     and every bare tmux resolves inside it and nowhere else.
//
// A child test binary this one re-executes inherits the same directory rather
// than making another; PrivateDirEnv is how it tells the two apart.
func init() {
	isolate()
}

var (
	privateDir string
	// ownsPrivateDir is false in a child that inherited its parent's
	// directory: the parent tears it down, once.
	ownsPrivateDir bool
)

func isolate() {
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	if privateDir != "" {
		os.Setenv("TMUX_TMPDIR", privateDir)
		return
	}
	if dir := os.Getenv(tmuxguard.PrivateDirEnv); dir != "" && os.Getenv("TMUX_TMPDIR") == dir {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			privateDir = dir
			return
		}
	}
	dir, err := os.MkdirTemp(tempRoot(), "gitmux-")
	if err != nil {
		panic("tmuxtest: cannot create a private TMUX_TMPDIR: " + err.Error())
	}
	privateDir, ownsPrivateDir = dir, true
	os.Setenv("TMUX_TMPDIR", dir)
	os.Setenv(tmuxguard.PrivateDirEnv, dir)
}

// tempRoot is where the private directory goes. A unix socket path is capped
// near 104 bytes, and a deep TMPDIR plus tmux-<uid>/gitest-<family>-<suffix>
// can run past it, so a long one falls back to /tmp.
func tempRoot() string {
	if root := os.TempDir(); len(root) <= 40 {
		return root
	}
	return "/tmp"
}

// PrivateDir is this run's TMUX_TMPDIR.
func PrivateDir() string {
	return privateDir
}

// SocketPath is where tmux puts the server a -L socket name starts: inside
// PrivateDir. Tests that fake the TMUX a pane would carry build it from here,
// never from a literal /tmp/tmux-<uid>.
func SocketPath(socket string) string {
	return filepath.Join(tmuxguard.SocketDir(), socket)
}

// ScrubEnv is env made safe to hand a child process: TMUX and TMUX_PANE
// dropped, TMUX_TMPDIR forced to this run's private directory. Every test that
// builds a child's environment from os.Environ() passes it through here;
// TestNoUnscrubbedEnviron enforces that.
func ScrubEnv(env []string) []string {
	kept := make([]string, 0, len(env)+2)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "TMUX", "TMUX_PANE", "TMUX_TMPDIR", tmuxguard.PrivateDirEnv:
			continue
		}
		kept = append(kept, kv)
	}
	return append(kept, "TMUX_TMPDIR="+privateDir, tmuxguard.PrivateDirEnv+"="+privateDir)
}

// Environ is ScrubEnv(os.Environ()).
func Environ() []string {
	return ScrubEnv(os.Environ())
}

// Check fails the test if a tmux command on socket would reach a live server:
// the socket resolves into a live socket directory, or TMUX points at one.
func Check(t testing.TB, socket string) {
	t.Helper()
	CheckArgs(t, "-L", socket)
}

// CheckArgs is Check for a whole tmux argv (the arguments after the binary).
func CheckArgs(t testing.TB, args ...string) {
	t.Helper()
	if err := tmuxguard.Check(args); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tmuxguard.Resolve(args), privateDir+string(filepath.Separator)) {
		t.Fatalf("tmuxtest: tmux %s resolves to %s, outside this run's private TMUX_TMPDIR %s",
			strings.Join(args, " "), tmuxguard.Resolve(args), privateDir)
	}
}

// Main is the TestMain of a package whose tests can reach tmux, directly or
// through a child process: isolated before, torn down after.
func Main(m *testing.M) {
	os.Exit(Run(m.Run))
}

// Run wraps a test run in isolation: strays from earlier runs swept first, and
// afterwards every server this run started in its private directory killed,
// the clients that outlived them collected, and the directory removed.
func Run(run func() int) int {
	isolate()
	ReapStrays()
	defer teardown()
	return run()
}

// teardown ends everything in the private directory. It addresses each server
// by -S and the full path, never by name or by environment: the directory is
// this run's own, so nothing in it can be anybody else's.
func teardown() {
	if !ownsPrivateDir {
		return
	}
	dir := filepath.Join(privateDir, fmt.Sprintf("tmux-%d", os.Getuid()))
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			_ = exec.Command("tmux", "-S", filepath.Join(dir, entry.Name()), "kill-server").Run()
		}
	}
	for _, c := range Clients() {
		if path, ok := clientSocketPath(c); ok && strings.HasPrefix(path, privateDir+string(filepath.Separator)) {
			_ = syscall.Kill(c.PID, syscall.SIGKILL)
		}
	}
	_ = os.RemoveAll(privateDir)
}
