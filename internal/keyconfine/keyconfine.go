// Package keyconfine starts a process with the board's key directory hidden
// from it and from everything it starts.
//
// Claude Code is kept out of that directory by the denials Gate Inbox puts in
// its settings (hooks.KeyDirDenials): a permission rule for its file tools and
// a sandbox rule for its shell. No other CLI the board launches reads those
// settings, and most have nothing like them: pi has no permission system and
// no sandbox, codex's sandbox limits writes rather than reads, and opencode
// runs its shells in a server the board starts. A session on any of them
// could read the parent-seal keys and forge the messages a parent sends its
// children.
//
// So every such launch runs under the operating system's own confinement,
// which applies to the whole process tree and needs nothing from the CLI:
// bubblewrap on Linux, mounting an empty read-only directory over the keys,
// and sandbox-exec on macOS, denying reads and writes under them. Where
// neither works the launch is refused: starting the session unconfined is the
// gap this closes.
//
// It does not hold against a process that asks something outside the
// confinement to read the keys for it, such as the tmux server or the board
// itself. A full sandbox for the session's shell is what closes that.
package keyconfine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Mechanism is how a process is confined.
type Mechanism string

const (
	// Bubblewrap mounts an empty directory over the keys (Linux).
	Bubblewrap Mechanism = "bwrap"
	// SandboxExec denies file access under the keys (macOS).
	SandboxExec Mechanism = "sandbox-exec"
)

// probeTimeout bounds the one trial run that checks a mechanism works here.
const probeTimeout = 5 * time.Second

var (
	probeOnce sync.Once
	probed    Mechanism
	probeErr  error
)

// available is the mechanism this machine can confine with, found once per
// process. Tests replace it.
var available = func() (Mechanism, error) {
	probeOnce.Do(func() { probed, probeErr = probe(runtime.GOOS) })
	return probed, probeErr
}

// Unconfined is a mechanism that runs argv as given. Only tests use it, through
// UseForTests: a board never chooses it, and no environment variable or
// setting reaches it, since a confined session could set either for the
// sessions it spawns.
const Unconfined Mechanism = "unconfined"

// UseForTests makes every later Argv use mechanism, or refuse with err, in
// place of what this machine has, and returns the function that undoes it.
// A test package whose launches are about something else passes Unconfined,
// so its plans do not depend on the host having bwrap.
func UseForTests(mechanism Mechanism, err error) (restore func()) {
	old := available
	available = func() (Mechanism, error) { return mechanism, err }
	return func() { available = old }
}

// Available says why a process cannot be confined here, or nil when it can.
func Available() error {
	_, err := available()
	return err
}

// probe finds the mechanism for goos and runs it once, since an installed
// bwrap can still be refused user namespaces, and a launch that dies in its
// pane on that error reads far worse than one refused with a reason.
func probe(goos string) (Mechanism, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	switch goos {
	case "linux":
		bin, err := exec.LookPath(string(Bubblewrap))
		if err != nil {
			return "", errors.New("bwrap (bubblewrap) is not installed")
		}
		dir := os.TempDir()
		if out, err := exec.CommandContext(ctx, bin, "--dev-bind", "/", "/", "--tmpfs", dir, "--", "/bin/true").CombinedOutput(); err != nil {
			return "", fmt.Errorf("bwrap cannot run here: %s", firstLine(out, err))
		}
		return Bubblewrap, nil
	case "darwin":
		bin, err := exec.LookPath(string(SandboxExec))
		if err != nil {
			return "", errors.New("sandbox-exec is not available")
		}
		if out, err := exec.CommandContext(ctx, bin, "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput(); err != nil {
			return "", fmt.Errorf("sandbox-exec cannot run here: %s", firstLine(out, err))
		}
		return SandboxExec, nil
	}
	return "", fmt.Errorf("no confinement is implemented for %s", goos)
}

func firstLine(out []byte, err error) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if line == "" {
		return err.Error()
	}
	return line
}

// Argv is argv run with keyDir hidden. keep are paths under keyDir the
// process still needs to read; they are visible read-only, and one that does
// not exist yet stays absent. keyDir need not exist.
func Argv(keyDir string, keep []string, argv []string) ([]string, error) {
	if len(argv) == 0 {
		return nil, errors.New("keyconfine: nothing to run")
	}
	mechanism, err := available()
	if err != nil {
		return nil, err
	}
	return wrap(mechanism, resolve(keyDir), keep, argv)
}

// resolve is keyDir with its symlinks resolved, since both mechanisms act on
// the real path: on macOS the temp directory is a symlink, and a rule for the
// link matches nothing.
func resolve(keyDir string) string {
	keyDir = filepath.Clean(keyDir)
	if real, err := filepath.EvalSymlinks(keyDir); err == nil {
		return real
	}
	parent, base := filepath.Split(keyDir)
	if real, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(real, base)
	}
	return keyDir
}

func wrap(mechanism Mechanism, keyDir string, keep []string, argv []string) ([]string, error) {
	kept := make([]string, 0, len(keep))
	for _, path := range keep {
		path = filepath.Join(keyDir, mustRel(keyDir, path))
		if path != keyDir {
			kept = append(kept, path)
		}
	}
	switch mechanism {
	case Unconfined:
		return argv, nil
	case Bubblewrap:
		// Remounted read-only only after the binds, which need mount points
		// made in the tmpfs; read-only so a write fails rather than landing
		// in a tmpfs that vanishes with the session.
		out := []string{string(Bubblewrap), "--dev-bind", "/", "/", "--die-with-parent", "--tmpfs", keyDir}
		for _, path := range kept {
			out = append(out, "--ro-bind-try", path, path)
		}
		out = append(out, "--remount-ro", keyDir, "--")
		return append(out, argv...), nil
	case SandboxExec:
		return append([]string{string(SandboxExec), "-p", profile(keyDir, kept)}, argv...), nil
	}
	return nil, fmt.Errorf("keyconfine: unknown mechanism %q", mechanism)
}

// mustRel is path relative to keyDir, whether path was given relative or
// absolute; a path outside keyDir names nothing to keep and comes back ".".
func mustRel(keyDir, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(keyDir, path)
	}
	rel, err := filepath.Rel(keyDir, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "."
	}
	return rel
}

// profile is the sandbox-exec policy: everything allowed, then file access
// under keyDir denied, then reads of the kept paths allowed again. A later
// rule wins over an earlier one.
func profile(keyDir string, kept []string) string {
	var b strings.Builder
	b.WriteString("(version 1)(allow default)")
	b.WriteString("(deny file-read* file-write* (subpath " + sbplString(keyDir) + "))")
	for _, path := range kept {
		b.WriteString("(allow file-read* (subpath " + sbplString(path) + "))")
	}
	return b.String()
}

func sbplString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
