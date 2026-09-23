package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// walFrames reports how many frames the write-ahead log file has room for,
// which is the most it has ever held rather than what is pending now: sqlite
// rewinds a checkpointed log to its start and writes over it instead of
// shrinking the file. That high-water mark is exactly what distinguishes a
// log nothing is checkpointing from one sqlite resets every thousand frames.
func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func walFrames(t *testing.T, dbPath string) int64 {
	t.Helper()
	info, err := os.Stat(dbPath + "-wal")
	if err != nil {
		return 0
	}
	const header, frameHeader, pageSize = 32, 24, 4096
	if info.Size() <= header {
		return 0
	}
	return (info.Size() - header) / (frameHeader + pageSize)
}

// churn commits one row at a time, which is the shape the board writes in and
// the shape that trips the inline checkpoint.
func churn(t *testing.T, st *Store, prefix string, n int) {
	t.Helper()
	for i := range n {
		if err := st.SetSetting(fmt.Sprintf("%s%d", prefix, i), "v"); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
}

// The database file is what tells: pages a transaction writes live in the log
// until a checkpoint folds them in, so a file that has not grown across
// thousands of new rows is a file no commit checkpointed. The log's own
// length proves nothing here -- it keeps growing either way, because sqlite
// rewinds it only when a checkpoint completes with no reader holding it.
func TestCommitsStopCheckpointingOnceCheckpointsAreDeferred(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "deferred.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.DeferCheckpoints(); err != nil {
		t.Fatalf("DeferCheckpoints: %v", err)
	}
	before := fileSize(t, path)
	// Comfortably past sqlite's default trigger of 1000 frames, so a run
	// that still checkpoints is certain to have done so several times.
	churn(t, st, "a", 2500)

	if after := fileSize(t, path); after != before {
		t.Fatalf("database grew %d -> %d bytes: a commit checkpointed, so the fsync is still on the caller's path", before, after)
	}
	if frames := walFrames(t, path); frames == 0 {
		t.Fatal("the log never grew, so the writes did not land where this test needs them")
	}
}

// The log has to be folded back in by somebody, or it grows until the disk is
// full. This is the half of the bargain DeferCheckpoints depends on.
//
// What proves it is reuse rather than the pragma's own counters: sqlite keeps
// reporting a checkpointed log at its full length until a writer rewinds it,
// so the honest question is whether the next writes land in the space the
// checkpoint freed or on the end of a file that keeps growing.
func TestCheckpointDrainsTheLog(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "drained.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.DeferCheckpoints(); err != nil {
		t.Fatalf("DeferCheckpoints: %v", err)
	}
	churn(t, st, "a", 2500)
	grown := walFrames(t, path)
	if grown <= 1000 {
		t.Fatalf("log only reached %d frames; this test needs one nothing checkpointed", grown)
	}

	if err := st.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	churn(t, st, "b", 500)

	if after := walFrames(t, path); after > grown {
		t.Fatalf("log grew from %d to %d frames after a checkpoint: nothing was folded back in, so the space could not be reused", grown, after)
	}
}
