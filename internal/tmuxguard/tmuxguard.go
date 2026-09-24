// Package tmuxguard keeps a test binary off the operator's live tmux server.
//
// A test process inherits TMUX from whatever pane `go test` was typed into,
// and tmux honours TMUX ahead of TMUX_TMPDIR: a bare `tmux kill-server` in a
// test cleanup, run from an agent's pane, killed the operator's live server
// twice on 2026-09-23. internal/tmuxtest isolates every test binary that can
// reach tmux, and this package is the backstop inside the code that runs tmux:
// in a test binary, a command whose socket resolves into a live socket
// directory panics instead of running.
//
// It is ordinary (non-test) code because the driver lives in ordinary code.
// Outside a test binary every check here returns immediately.
package tmuxguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PrivateDirEnv names the TMUX_TMPDIR internal/tmuxtest created for this run.
// A child test binary inherits both, and seeing them equal is how it knows its
// TMUX_TMPDIR is the private one rather than the operator's own.
const PrivateDirEnv = "GATE_INBOX_TEST_TMUX_TMPDIR"

// liveDirs are the socket directories a live server may be listening in,
// captured from the environment the test binary started with, before
// internal/tmuxtest replaced it: tmux's default directory, the directory an
// inherited TMUX_TMPDIR names, and the one an inherited TMUX points into.
var liveDirs = inheritedLiveDirs()

func inheritedLiveDirs() []string {
	dirs := []string{DefaultSocketDir()}
	private := os.Getenv(PrivateDirEnv)
	if tmpdir := os.Getenv("TMUX_TMPDIR"); tmpdir != "" && tmpdir != private {
		dirs = append(dirs, socketDirIn(tmpdir))
	}
	if path, _, _ := strings.Cut(os.Getenv("TMUX"), ","); path != "" {
		if private == "" || !within(path, socketDirIn(private)) {
			dirs = append(dirs, filepath.Dir(path))
		}
	}
	return dirs
}

// DefaultSocketDir is where tmux puts its sockets when TMUX_TMPDIR is unset:
// the directory the operator's default server listens in.
func DefaultSocketDir() string {
	return socketDirIn("/tmp")
}

// SocketDir is the directory a -L socket name resolves into under the current
// environment.
func SocketDir() string {
	tmpdir := os.Getenv("TMUX_TMPDIR")
	if tmpdir == "" {
		tmpdir = "/tmp"
	}
	return socketDirIn(tmpdir)
}

func socketDirIn(tmpdir string) string {
	return filepath.Join(tmpdir, fmt.Sprintf("tmux-%d", os.Getuid()))
}

// Resolve is the socket path tmux would connect to for this argv (the
// arguments after the binary): -S wins, then -L inside SocketDir, then the
// path TMUX carries, then the default server.
func Resolve(args []string) string {
	// Server flags come first; the first thing that is not one is the
	// command, and nothing after it names a server.
	// tmux keeps the last of each flag, and -S beats -L whatever the order.
	var path, label string
	for i := 0; i+1 < len(args) && strings.HasPrefix(args[i], "-"); i++ {
		switch args[i] {
		case "-S":
			i++
			path = args[i]
		case "-L":
			i++
			label = args[i]
		case "-f", "-T":
			i++
		}
	}
	if path != "" {
		return path
	}
	if label != "" {
		return filepath.Join(SocketDir(), label)
	}
	if path, _, _ := strings.Cut(os.Getenv("TMUX"), ","); path != "" {
		return path
	}
	return filepath.Join(SocketDir(), "default")
}

// Check reports why a tmux command with these arguments would reach a live
// server, or nil when it would not.
func Check(args []string) error {
	if path, _, _ := strings.Cut(os.Getenv("TMUX"), ","); path != "" && live(path) {
		return fmt.Errorf("tmuxguard: TMUX=%q names a live tmux server; a test must never inherit it", os.Getenv("TMUX"))
	}
	if path := Resolve(args); live(path) {
		return fmt.Errorf("tmuxguard: tmux %s resolves to %s, a live tmux socket directory; tests use a private TMUX_TMPDIR (internal/tmuxtest)",
			strings.Join(args, " "), path)
	}
	return nil
}

// Enforce panics on a command Check refuses, and only in a test binary. A
// panic rather than an error because the callers are many and some discard
// the error; a test that got this far is already wrong.
func Enforce(args []string) {
	if !testing.Testing() {
		return
	}
	if err := Check(args); err != nil {
		panic(err)
	}
}

// Err is Enforce for a caller that can return an error instead.
func Err(args []string) error {
	if !testing.Testing() {
		return nil
	}
	return Check(args)
}

func live(path string) bool {
	for _, dir := range liveDirs {
		if within(path, dir) {
			return true
		}
	}
	return false
}

func within(path, dir string) bool {
	path, dir = canonical(path), canonical(dir)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// canonical resolves symlinks in the deepest existing ancestor, so /tmp and
// macOS's /private/tmp compare equal.
func canonical(path string) string {
	path = filepath.Clean(path)
	rest := ""
	for dir := path; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		if dir == filepath.Dir(dir) {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}
