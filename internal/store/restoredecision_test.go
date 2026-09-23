package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestRestoreDecisionsSurviveTheProcessThatWroteThem(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	died := time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)
	if err := st.SetRestoreDecided(map[string]time.Time{"a": died}); err != nil {
		t.Fatalf("write: %v", err)
	}
	st.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	decided, err := reopened.RestoreDecided()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got, ok := decided["a"]; !ok || !got.Equal(died) {
		t.Fatalf("expected a settled at %v, got %v (present=%v)", died, got, ok)
	}
}

func TestAnEmptySetClearsTheLedger(t *testing.T) {
	st, err := Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.SetRestoreDecided(map[string]time.Time{"a": time.Now()}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := st.SetRestoreDecided(nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	decided, err := st.RestoreDecided()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(decided) != 0 {
		t.Fatalf("expected an empty ledger, got %+v", decided)
	}
}
