package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// A row from before operator_at existed has no record of its last operator
// turn, and is backfilled with its last status change, the latest that turn
// can have been. Rows made after the column arrived keep their zero on every
// later start: backfilling them too would stamp what their agent last did.
func TestOperatorAtIsBackfilledOnlyWhenTheColumnArrives(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.CreateSession(sample("old", "g1")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`ALTER TABLE sessions DROP COLUMN operator_at`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
	old, err := reopened.Get("old")
	if err != nil {
		t.Fatal(err)
	}
	if old.LastStatusAt.IsZero() || !old.OperatorAt.Equal(old.LastStatusAt) {
		t.Fatalf("migrated row's operator turn = %v, want its last status change %v", old.OperatorAt, old.LastStatusAt)
	}

	if err := reopened.CreateSession(sample("new", "g1")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.UpdateStatus("new", "finished"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.addOperatorAt(); err != nil {
		t.Fatalf("a later start: %v", err)
	}
	fresh, err := reopened.Get("new")
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.OperatorAt.IsZero() {
		t.Fatalf("a later start backfilled a new row's operator turn to %v", fresh.OperatorAt)
	}
}

func TestSetOperatorAt(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0)
	if err := st.SetOperatorAt("a", at); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	if !got.OperatorAt.Equal(at) {
		t.Fatalf("OperatorAt = %v, want %v", got.OperatorAt, at)
	}
	sessions, err := st.ListSessions(false)
	if err != nil || len(sessions) != 1 || !sessions[0].OperatorAt.Equal(at) {
		t.Fatalf("listed %+v, %v; want the stamp", sessions, err)
	}
	if err := st.SetOperatorAt("gone", at); !errors.Is(err, ErrSessionGone) {
		t.Fatalf("stamping a missing row = %v, want ErrSessionGone", err)
	}
}
