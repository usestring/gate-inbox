package managerbuild

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func recordFile(t *testing.T, dir, digest string, since time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	line := digest + " " + since.UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(line), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// The board runs under `go run`, so by the time an old MCP server asks this
// question its own path has been deleted for days. Reading /proc/self/exe
// is what makes the answer available anyway.
func TestFingerprintReadsTheRunningBinary(t *testing.T) {
	if got := Fingerprint(); len(got) != 64 {
		t.Fatalf("Fingerprint = %q, want a 64-character sha256", got)
	}
	if Fingerprint() != Fingerprint() {
		t.Fatal("Fingerprint changed between calls; a process cannot change its own executable")
	}
}

func TestRecordWritesTheRunningBuild(t *testing.T) {
	dir := t.TempDir()
	if err := Record(dir); err != nil {
		t.Fatalf("Record: %v", err)
	}
	digest, since, ok := read(dir)
	if !ok {
		t.Fatal("read found no record after Record")
	}
	if digest != Fingerprint() {
		t.Fatalf("recorded %q, want the running binary %q", digest, Fingerprint())
	}
	if time.Since(since) > time.Minute {
		t.Fatalf("recorded time %v is not now", since)
	}
}

// The board restarts many times a day without being rebuilt. If each start
// reset the clock, a session that has been behind for a week would report
// being behind since the last restart, which is the number a person would
// actually act on.
func TestRecordKeepsTheFirstTimeAnUnchangedBuildAppeared(t *testing.T) {
	dir := t.TempDir()
	first := time.Now().Add(-72 * time.Hour)
	recordFile(t, dir, Fingerprint(), first)

	if err := Record(dir); err != nil {
		t.Fatalf("Record: %v", err)
	}

	_, since, ok := read(dir)
	if !ok {
		t.Fatal("read found no record")
	}
	if since.Sub(first).Abs() > time.Second {
		t.Fatalf("since = %v, want the original %v", since, first)
	}
}

// A rebuild is a different build, and its clock starts now.
func TestRecordRestartsTheClockOnANewBuild(t *testing.T) {
	dir := t.TempDir()
	recordFile(t, dir, strings.Repeat("a", 64), time.Now().Add(-72*time.Hour))

	if err := Record(dir); err != nil {
		t.Fatalf("Record: %v", err)
	}

	_, since, ok := read(dir)
	if !ok {
		t.Fatal("read found no record")
	}
	if time.Since(since) > time.Minute {
		t.Fatalf("since = %v, want now", since)
	}
}

func TestStaleSinceReportsHowFarBehind(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	recordFile(t, dir, strings.Repeat("b", 64), now.Add(-6*24*time.Hour))

	behind, stale := StaleSince(dir, now)

	if !stale {
		t.Fatal("StaleSince reported current against a different board build")
	}
	if behind.Round(time.Hour) != 6*24*time.Hour {
		t.Fatalf("behind = %v, want 6 days", behind)
	}
}

func TestStaleSinceIsQuietOnTheBoardsOwnBuild(t *testing.T) {
	dir := t.TempDir()
	recordFile(t, dir, Fingerprint(), time.Now().Add(-72*time.Hour))

	if _, stale := StaleSince(dir, time.Now()); stale {
		t.Fatal("StaleSince flagged the board's own build")
	}
}

// Everything unknown has to read as current. A warning a session cannot act
// on, raised because a board has not started since this shipped, is the
// warning it learns to ignore.
func TestStaleSinceIsQuietWithoutARecord(t *testing.T) {
	if _, stale := StaleSince(t.TempDir(), time.Now()); stale {
		t.Fatal("StaleSince flagged a session with no board record")
	}
}

func TestStaleSinceIsQuietOnAnUnreadableRecord(t *testing.T) {
	for name, content := range map[string]string{
		"empty":       "",
		"no stamp":    strings.Repeat("c", 64) + "\n",
		"bad stamp":   strings.Repeat("c", 64) + " not-a-time\n",
		"no fingerpr": " 2026-09-09T00:00:00Z\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, stale := StaleSince(dir, time.Now()); stale {
				t.Fatalf("StaleSince flagged a session on an unreadable record %q", content)
			}
		})
	}
}

// A board whose clock went backwards, or a record copied from another
// machine, must not produce a negative age in the notice.
func TestStaleSinceNeverReportsNegativeAge(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	recordFile(t, dir, strings.Repeat("d", 64), now.Add(time.Hour))

	behind, stale := StaleSince(dir, now)

	if !stale {
		t.Fatal("StaleSince reported current against a different board build")
	}
	if behind < 0 {
		t.Fatalf("behind = %v, want it clamped to zero", behind)
	}
}

// The mechanism, pinned: on Linux the digest must come from /proc/self/exe,
// not from the path os.Executable reports. A `go run` board's binary is
// deleted when the run exits, so an old server asking this question has no
// readable path of its own left -- /proc still opens the inode it runs.
func TestFingerprintComesFromProcSelfExe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("no /proc on this platform")
	}
	if got, want := Fingerprint(), digestFile("/proc/self/exe"); got != want {
		t.Fatalf("Fingerprint = %q, want the /proc/self/exe digest %q", got, want)
	}
}
