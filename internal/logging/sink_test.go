package logging

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dirBytes is everything under dir, which is the number the total cap is
// about: a log that cannot fill the disk is one whose whole directory is
// bounded, not one whose active file is.
func dirBytes(t *testing.T, dir string) int64 {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}

// waitFor polls a condition rather than sleeping on it: lumberjack does its
// compaction and its deletions on a goroutine of its own, so the state a
// rotation leaves behind arrives after the write that caused it returns.
func waitFor(t *testing.T, why string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

func TestRotatesCompressesAndKeepsTheTotalUnderTheCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate-inbox.log")
	logger, err := Open(Options{
		Path: path, Level: LevelInfo,
		MaxSizeMB: 1, MaxBackups: 2, MaxTotalMB: 3, Compress: true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Highly compressible on purpose: the point is that a rotated file is
	// gzipped, and a gzip that barely shrinks proves nothing.
	filler := strings.Repeat("a", 512)
	for i := 0; i < 12000; i++ {
		logger.Info("filler", "i", i, "pad", filler)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	waitFor(t, "a compressed backup", func() bool {
		for _, name := range names(t, dir) {
			if strings.HasSuffix(name, ".gz") {
				return true
			}
		}
		return false
	})
	waitFor(t, "the backup count to settle", func() bool {
		backups := 0
		for _, name := range names(t, dir) {
			if name != "gate-inbox.log" {
				backups++
			}
		}
		return backups <= 2
	})

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the active log is gone: %v", err)
	}
	if total := dirBytes(t, dir); total > 3<<20 {
		t.Fatalf("log directory is %d bytes, over the 3 MB cap: %v", total, names(t, dir))
	}

	// A compressed backup has to be readable, or the rotation lost the
	// history rather than archiving it.
	var gzipped string
	for _, name := range names(t, dir) {
		if strings.HasSuffix(name, ".gz") {
			gzipped = filepath.Join(dir, name)
		}
	}
	file, err := os.Open(gzipped)
	if err != nil {
		t.Fatalf("open %s: %v", gzipped, err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("gzip %s: %v", gzipped, err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %s: %v", gzipped, err)
	}
	if !strings.Contains(string(body), "msg=filler") {
		t.Fatalf("the compressed backup does not hold the records that rotated out")
	}
}

func TestPruneToTotalDeletesOldestFirstAndSparesTheActiveFile(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "gate-inbox.log")
	write := func(name string, size int, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		return path
	}
	write("gate-inbox.log", 400, 0)
	oldest := write("gate-inbox-2026-08-20T00-00-00.000.log.gz", 400, 3*time.Hour)
	middle := write("gate-inbox-2026-08-21T00-00-00.000.log.gz", 400, 2*time.Hour)
	newest := write("gate-inbox-2026-08-22T00-00-00.000.log", 400, time.Hour)
	// Nothing this log wrote, and therefore nothing it may delete.
	foreign := write("state.db", 4000, 4*time.Hour)

	removed := pruneToTotal(active, 1000)
	if len(removed) != 2 {
		t.Fatalf("removed %v, want the two oldest rotated files", removed)
	}
	for _, gone := range []string{oldest, middle} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s survived the sweep", filepath.Base(gone))
		}
	}
	for _, kept := range []string{active, newest, foreign} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s was deleted: %v", filepath.Base(kept), err)
		}
	}
}

func TestPruneToTotalLeavesADirectoryUnderTheCapAlone(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "gate-inbox.log")
	if err := os.WriteFile(active, make([]byte, 100), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	backup := filepath.Join(dir, "gate-inbox-2026-08-20T00-00-00.000.log.gz")
	if err := os.WriteFile(backup, make([]byte, 100), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if removed := pruneToTotal(active, 1000); removed != nil {
		t.Fatalf("pruneToTotal removed %v from a directory under the cap", removed)
	}
}

// A cap lowered between runs is the case MaxBackups arithmetic cannot cover:
// the files are already on disk when the new limit arrives.
func TestOpenTrimsADirectoryLeftOversizedByAnEarlierRun(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "gate-inbox.log")
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("gate-inbox-2026-08-%02dT00-00-00.000.log", 10+i)
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, 1<<20), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	logger, err := Open(Options{Path: active, Level: LevelInfo, MaxSizeMB: 1, MaxBackups: 2, MaxTotalMB: 3})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer logger.Close()
	if total := dirBytes(t, dir); total > 3<<20 {
		t.Fatalf("Open left %d bytes on disk, over the 3 MB cap", total)
	}
}

// The render loop and the poll loop must never wait on the disk. A writer
// that has fallen behind drops lines; it does not block its caller.
func TestWriteDropsRatherThanBlocksWhenTheWriterIsBehind(t *testing.T) {
	stalled := &sink{queue: make(chan []byte, 1)}
	stalled.queue <- []byte("already queued")

	returned := make(chan struct{})
	go func() {
		stalled.Write([]byte("this one has nowhere to go"))
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on a full queue")
	}
	if dropped := stalled.dropped.Load(); dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
}

func TestBelongsToMatchesOnlyThisLogsFiles(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"gate-inbox.log", true},
		{"gate-inbox-2026-08-20T00-00-00.000.log", true},
		{"gate-inbox-2026-08-20T00-00-00.000.log.gz", true},
		{"gate-inbox.log.old", false},
		{"state.db", false},
		{"config.toml", false},
		{"other-2026-08-20T00-00-00.000.log", false},
	}
	for _, tc := range cases {
		if got := belongsTo(tc.name, "gate-inbox.log", "gate-inbox", ".log"); got != tc.want {
			t.Fatalf("belongsTo(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The poll loop has no stop of its own, so it is still logging while
// shutdown runs. Closing must cost it a dropped line, never a panic.
func TestLoggingAfterCloseIsDroppedRatherThanFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gate-inbox.log")
	logger, err := Open(Options{Path: path, Level: LevelInfo, MaxSizeMB: 1, MaxBackups: 1, MaxTotalMB: 4})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	restore := SetDefault(logger)
	defer SetDefault(restore)

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for i := 0; i < 20000; i++ {
			Info("poll pass", "i", i)
		}
	}()
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-stopped

	// A second Close is what a deferred close beside an explicit one looks
	// like, and it must not close an already-closed channel.
	if err := logger.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Nothing drains the queue once the writer has stopped, so the records
	// that arrived after the close were discarded rather than written --
	// which is the degradation this is here to prove, panicking aside.
	if logger.Dropped() == 0 {
		t.Fatal("records logged after the close were not accounted as dropped")
	}
}

// lumberjack stamps a .gz with its compression time and compresses
// newest-first, so mtime order can be the reverse of history order. Age has
// to come from the timestamp in the name or the sweep deletes the newest
// backup first.
func TestPruneToTotalOrdersByTheTimestampInTheNameNotTheMtime(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "gate-inbox.log")
	write := func(name string, size int, mtimeAge time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		when := time.Now().Add(-mtimeAge)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		return path
	}
	write("gate-inbox.log", 100, 0)
	// The mtimes are deliberately the inverse of the names: this is what a
	// mill run that compressed both in one batch leaves behind.
	older := write("gate-inbox-2026-08-20T00-00-00.000.log.gz", 500, time.Minute)
	newer := write("gate-inbox-2026-08-24T00-00-00.000.log.gz", 500, time.Hour)

	removed := pruneToTotal(active, 700)
	if len(removed) != 1 || removed[0] != older {
		t.Fatalf("removed %v, want only the file the name says is oldest (%s)", removed, filepath.Base(older))
	}
	if _, err := os.Stat(newer); err != nil {
		t.Fatalf("the newest backup was deleted: %v", err)
	}
}

// lumberjack's own mill deletes backups too, so the sweep has to expect a
// file to be gone already; counting it anyway takes a live backup with it.
func TestPruneToTotalDoesNotOverDeleteWhenAFileIsAlreadyGone(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, "gate-inbox.log")
	if err := os.WriteFile(active, make([]byte, 100), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	var backups []string
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("gate-inbox-2026-08-2%dT00-00-00.000.log.gz", i)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, 500), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		backups = append(backups, path)
	}
	// The oldest is milled away between the readdir and the remove.
	if err := os.Remove(backups[0]); err != nil {
		t.Fatalf("remove: %v", err)
	}
	pruneToTotal(active, 1100)
	if _, err := os.Stat(backups[2]); err != nil {
		t.Fatalf("the newest backup was deleted to make up for one already gone: %v", err)
	}
}

func TestOwnsMatchesOnlyThisLogsFiles(t *testing.T) {
	active := "/home/dev/.config/gate-inbox/logs/gate-inbox.log"
	for name, want := range map[string]bool{
		"gate-inbox.log": true,
		"gate-inbox-2026-08-20T00-00-00.000.log.gz": true,
		"state.db":    false,
		"config.toml": false,
	} {
		if got := Owns(active, name); got != want {
			t.Fatalf("Owns(%q) = %v, want %v", name, got, want)
		}
	}
}

// A file larger than the whole cap would make the cap unenforceable, since
// the active file is never deleted.
func TestResolveClampsAFileLargerThanTheWholeCap(t *testing.T) {
	t.Setenv(LevelEnv, "")
	t.Setenv(FileEnv, "")
	opts, err := Resolve(t.TempDir(), Settings{MaxSizeMB: 100, MaxTotalMB: 48})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if opts.MaxSizeMB > opts.MaxTotalMB {
		t.Fatalf("max_size_mb %d exceeds the %d MB cap", opts.MaxSizeMB, opts.MaxTotalMB)
	}
}
