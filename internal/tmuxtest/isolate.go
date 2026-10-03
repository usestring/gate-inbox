package tmuxtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/usestring/gate-inbox/internal/envname"
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
//   - the GATE_INBOX_* variables a managed session runs with are unset. Run
//     from inside a board's session, a test inherited that session's exit and
//     status files: a fake agent's launch script exiting 0 wrote the live
//     board's record, and the board reported the session that ran the tests
//     as exited while it was still working;
//   - TMUX_TMPDIR is a fresh directory of this run's own, so every -L socket
//     and every bare tmux resolves inside it and nowhere else;
//   - the tmux found first on PATH is a shim that starts every server with
//     -f /dev/null, so no test server loads the operator's config: see
//     shimTmux.
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

// sessionEnv is the environment that ties a process to a live board session.
var sessionEnv = []string{envname.SessionID, envname.Executable, envname.StatusFile, envname.ExitFile}

func isolate() {
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	for _, name := range sessionEnv {
		os.Unsetenv(name)
	}
	if privateDir != "" {
		os.Setenv("TMUX_TMPDIR", privateDir)
		prependShimDir()
		return
	}
	if dir := os.Getenv(tmuxguard.PrivateDirEnv); dir != "" && os.Getenv("TMUX_TMPDIR") == dir {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			privateDir = dir
			prependShimDir()
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
	writeOwner(dir)
	shimTmux(dir)
}

// ownerFile is locked for as long as the test process that created a private
// directory is alive. ReapStrays tries the lock to tell a directory whose run
// died -- a -timeout panic or a SIGKILL skips every cleanup and TestMain's
// teardown -- from one a live run still owns. A lock rather than a pid because
// the kernel drops it when the process dies, and because a pid read from
// another pid namespace (a sandboxed run) names the wrong process.
const ownerFile = "owner.lock"

// owner holds the lock; it is never closed, so the lock lasts the process.
var owner *os.File

// writeOwner locks the file before it takes its name, so no reaper can open
// it unlocked. Go opens with O_CLOEXEC, so no child inherits the lock.
func writeOwner(dir string) {
	f, err := os.CreateTemp(dir, "owner-")
	if err != nil {
		return
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return
	}
	if os.Rename(f.Name(), filepath.Join(dir, ownerFile)) != nil {
		f.Close()
		return
	}
	owner = f
}

// shimDir is where the tmux shim lives inside a private directory.
func shimDir(dir string) string {
	return filepath.Join(dir, "bin")
}

// shimTmux puts a tmux ahead of the real one on PATH that adds -f /dev/null.
//
// Without it every test server loads ~/.tmux.conf, and an operator's config
// can run anything: tmux-continuum, tmux-resurrect and their kin fork a steady
// stream of run-shell jobs per server. Measured on the development host, those
// plugins alone added about 350 processes a second while tests ran.
//
// A shim rather than an argument at each call site because tests reach tmux
// three ways -- raw exec.Command("tmux", ...), the product driver, which
// resolves tmux with exec.LookPath, and child binaries that inherit this
// environment -- and only PATH reaches all three. The product's own argv is
// untouched: outside a test binary nothing puts the shim on PATH.
//
// -f only matters to the command that starts a server; a client of a running
// one ignores it, so adding it to every command is harmless.
func shimTmux(dir string) {
	real, err := exec.LookPath("tmux")
	if err != nil {
		// No tmux: the tests that need it skip.
		return
	}
	bin := shimDir(dir)
	if err := os.MkdirAll(bin, 0o700); err != nil {
		panic("tmuxtest: cannot create the tmux shim directory: " + err.Error())
	}
	quoted := "'" + strings.ReplaceAll(real, "'", `'\''`) + "'"
	script := "#!/bin/sh\nexec " + quoted + " -f /dev/null \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o700); err != nil {
		panic("tmuxtest: cannot write the tmux shim: " + err.Error())
	}
	prependShimDir()
}

// ShimPath is the tmux shim on this run's PATH, or "" when tmux is not
// installed.
func ShimPath() string {
	path := filepath.Join(shimDir(privateDir), "tmux")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// prependShimDir puts the shim first on PATH, once. A child that inherited its
// parent's environment already has it there.
func prependShimDir() {
	bin := shimDir(privateDir)
	if _, err := os.Stat(filepath.Join(bin, "tmux")); err != nil {
		return
	}
	path := os.Getenv("PATH")
	if first, _, _ := strings.Cut(path, string(os.PathListSeparator)); first == bin {
		return
	}
	os.Setenv("PATH", bin+string(os.PathListSeparator)+path)
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
		if slices.Contains(sessionEnv, key) {
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
