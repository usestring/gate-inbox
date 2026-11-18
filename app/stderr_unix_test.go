//go:build unix

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// A write to fd 2 while redirected lands in the file, not on the terminal,
// and the original fd 2 is back afterwards.
func TestRedirectStderrSendsFD2ToFile(t *testing.T) {
	dir := t.TempDir()
	before := fdTarget(t)

	restore := redirectStderr(dir)
	if _, err := unix.Write(2, []byte("node(1) MallocStackLogging: can't turn off\n")); err != nil {
		t.Fatal(err)
	}
	restore()

	got, err := os.ReadFile(filepath.Join(dir, StderrFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "MallocStackLogging") {
		t.Fatalf("file = %q, want the fd 2 write", got)
	}
	if after := fdTarget(t); after != before {
		t.Fatalf("fd 2 not restored: before %v, after %v", before, after)
	}
}

// An oversized file is truncated at the next start rather than growing forever.
func TestRedirectStderrTruncatesOversizedFile(t *testing.T) {
	defer func(limit int64) { stderrMaxBytes = limit }(stderrMaxBytes)
	stderrMaxBytes = 16
	dir := t.TempDir()
	path := filepath.Join(dir, StderrFileName)
	if err := os.WriteFile(path, make([]byte, stderrMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	redirectStderr(dir)()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > stderrMaxBytes {
		t.Fatalf("size = %d, want truncated", info.Size())
	}
}

// A directory that cannot be created leaves fd 2 untouched.
func TestRedirectStderrLeavesFD2OnFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before := fdTarget(t)
	redirectStderr(filepath.Join(blocker, "logs"))()
	if after := fdTarget(t); after != before {
		t.Fatalf("fd 2 changed on failure: before %v, after %v", before, after)
	}
}

func fdTarget(t *testing.T) [2]uint64 {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Fstat(2, &st); err != nil {
		t.Fatal(err)
	}
	return [2]uint64{uint64(st.Dev), uint64(st.Ino)}
}
