package launch

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBinary(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// The whole point: what the configs name must outlive the run that wrote
// them, so the running binary is copied somewhere fixed and that copy is
// what is named.
func TestInstallExecutableCopiesTheBinaryUnderTheHome(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(t.TempDir(), "go-build123", "b001", "exe", "gate-inbox")
	writeBinary(t, exe, "build one")

	got := installExecutable(exe, home)

	want := filepath.Join(home, "bin", "gate-inbox")
	if got != want {
		t.Fatalf("installExecutable = %q, want %q", got, want)
	}
	content, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "build one" {
		t.Fatalf("copy holds %q, want the running binary's bytes", content)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("copy mode = %v, want it executable", info.Mode())
	}
}

// A board that rebuilt overwrites the copy, which is what makes
// reconnecting a weeks-old session's MCP server land on current code.
func TestInstallExecutableOverwritesAnOlderBuild(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "bin", "gate-inbox")
	writeBinary(t, target, "build one")
	exe := filepath.Join(t.TempDir(), "gate-inbox")
	writeBinary(t, exe, "build two")

	if got := installExecutable(exe, home); got != target {
		t.Fatalf("installExecutable = %q, want %q", got, target)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "build two" {
		t.Fatalf("copy holds %q, want the newer build", content)
	}
}

// Rewriting an identical binary would give it a new inode on every board
// start for no reason, so identical bytes are left exactly as they are.
func TestInstallExecutableLeavesAnIdenticalCopyAlone(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "bin", "gate-inbox")
	writeBinary(t, target, "same bytes")
	before, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "gate-inbox")
	writeBinary(t, exe, "same bytes")

	if got := installExecutable(exe, home); got != target {
		t.Fatalf("installExecutable = %q, want %q", got, target)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("identical bytes were rewritten; want the file untouched")
	}
}

// The board is already the copy on a second run, and copying a file over
// itself is how a running binary gets truncated.
func TestInstallExecutableReturnsTheTargetWhenItIsAlreadyRunningIt(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "bin", "gate-inbox")
	writeBinary(t, target, "installed")

	if got := installExecutable(target, home); got != target {
		t.Fatalf("installExecutable = %q, want %q", got, target)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "installed" {
		t.Fatalf("copy holds %q, want it untouched", content)
	}
}

// `go run` deletes its build directory when the run exits, so a board
// started from one and asked for its own path after a restart has no
// source to copy. A copy an earlier run left is closer to current than a
// path that no longer resolves.
func TestInstallExecutableFallsBackToAnExistingCopy(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "bin", "gate-inbox")
	writeBinary(t, target, "left by an earlier run")
	gone := filepath.Join(t.TempDir(), "go-build999", "b001", "exe", "gate-inbox")

	if got := installExecutable(gone, home); got != target {
		t.Fatalf("installExecutable = %q, want the surviving copy %q", got, target)
	}
}

// With no source and no copy there is nothing honest to name, and the
// caller falls back to os.Executable() and then to the bare name.
func TestInstallExecutableReportsNothingUsable(t *testing.T) {
	home := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gate-inbox")

	if got := installExecutable(gone, home); got != "" {
		t.Fatalf("installExecutable = %q, want %q", got, "")
	}
}

// A non-executable file at the target is not a manager: naming it hands
// every spawned session a command that cannot run.
func TestInstallExecutableIgnoresANonExecutableTarget(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "bin", "gate-inbox")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(target, []byte("not a binary"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	gone := filepath.Join(t.TempDir(), "gate-inbox")

	if got := installExecutable(gone, home); got != "" {
		t.Fatalf("installExecutable = %q, want %q", got, "")
	}
}

// Reading the installed path must never install: an MCP server that has
// been running for weeks holds an OLD binary, and it calls Executable every
// time its agent spawns a session. If that installed, the first such spawn
// would overwrite the board's current copy with a stale one -- exactly the
// bug this file exists to close, running backwards.
func TestExecutableNamesTheInstalledCopyWithoutWritingIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GATE_INBOX_HOME", home)
	target := filepath.Join(home, "bin", "gate-inbox")
	writeBinary(t, target, "the board's build")

	if got := Executable(); got != target {
		t.Fatalf("Executable = %q, want the installed copy %q", got, target)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "the board's build" {
		t.Fatalf("copy holds %q; reading the path overwrote it", content)
	}
}

// Before any board has installed one there is nothing to name, and the
// running binary is the honest answer -- which is what every spawn used
// before this file existed.
func TestExecutableFallsBackToTheRunningBinary(t *testing.T) {
	t.Setenv("GATE_INBOX_HOME", t.TempDir())
	running, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	if got := Executable(); got != running {
		t.Fatalf("Executable = %q, want the running binary %q", got, running)
	}
}

// Install is the write half, and the board is the only caller.
func TestInstallPutsTheRunningBinaryWhereExecutableLooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GATE_INBOX_HOME", home)

	installed := Install()

	if want := filepath.Join(home, "bin", "gate-inbox"); installed != want {
		t.Fatalf("Install = %q, want %q", installed, want)
	}
	if got := Executable(); got != installed {
		t.Fatalf("Executable = %q, want the path Install just wrote %q", got, installed)
	}
	running, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	want, err := os.ReadFile(running)
	if err != nil {
		t.Skipf("ReadFile: %v", err)
	}
	got, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("installed %d bytes, want the running binary's %d", len(got), len(want))
	}
}
