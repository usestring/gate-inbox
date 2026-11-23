package store

import (
	"database/sql"
	"errors"
	"testing"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestLaunchSessionReservesIDBeforePaneExists(t *testing.T) {
	st := newTestStore(t)
	other, err := connect(st.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.db.Exec("PRAGMA busy_timeout=0"); err != nil {
		t.Fatal(err)
	}
	child := sample("child", "sample-repo")
	called := false
	err = st.LaunchSession(child, func() error {
		called = true
		if _, err := other.Get(child.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("uncommitted row visible to scan: %v", err)
		}
		var busy *sqlite.Error
		if err := other.CreateSession(sample(child.ID, "")); !errors.As(err, &busy) || busy.Code() != sqlite3.SQLITE_BUSY {
			t.Fatalf("adoption can write during launch: %v", err)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("launch = %v, called = %v", err, called)
	}
	if err := other.CreateSession(sample(child.ID, "")); err == nil {
		t.Fatal("adoption replaced the committed spawn")
	}
	got, err := other.Get(child.ID)
	if err != nil || got.Group != child.Group {
		t.Fatalf("spawn lost placement: %+v, %v", got, err)
	}
}

func TestFailedLaunchRollsBackPlacement(t *testing.T) {
	st := newTestStore(t)
	parent := sample("parent", "sample-repo")
	if err := st.CreateSession(parent); err != nil {
		t.Fatal(err)
	}
	child := sample("child", "")
	child.ParentID = parent.ID
	launchErr := errors.New("tmux launch failed")
	if err := st.LaunchSession(child, func() error { return launchErr }); !errors.Is(err, launchErr) {
		t.Fatalf("launch = %v", err)
	}
	if _, err := st.Get(child.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed launch left a row: %v", err)
	}
	if err := st.LaunchSession(child, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := st.LaunchSession(child, func() error {
		t.Fatal("duplicate row launched a pane")
		return nil
	}); err == nil {
		t.Fatal("duplicate row was accepted")
	}
}

func TestAgentLaunchChargesTheLastSlotOnlyOnceAcrossStores(t *testing.T) {
	st := newTestStore(t)
	root := sample("parent", "")
	if err := st.CreateSession(root); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"worker1", "worker2", "worker3", "worker4", "worker5"} {
		child := sample(id, "")
		child.SpawnedBy = root.ID
		if err := st.LaunchAgentSession(child, "", false, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	losingPane := sample("failed-pane", "")
	losingPane.SpawnedBy = root.ID
	launchErr := errors.New("could not create pane")
	if err := st.LaunchAgentSession(losingPane, "", false, func() error { return launchErr }); !errors.Is(err, launchErr) {
		t.Fatalf("failed launch: %v", err)
	}

	other, err := connect(st.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	for i, db := range []*Store{st, other} {
		go func() {
			<-start
			child := sample([]string{"candidate-a", "candidate-b"}[i], "")
			child.SpawnedBy = "worker1"
			outcomes <- db.LaunchAgentSession(child, "", false, func() error { return nil })
		}()
	}
	close(start)
	var launched, refused int
	for range 2 {
		switch err := <-outcomes; {
		case err == nil:
			launched++
		case errors.Is(err, ErrFanoutLimit):
			refused++
		default:
			t.Fatal(err)
		}
	}
	if launched != 1 || refused != 1 {
		t.Fatalf("launched %d and refused %d, want one each", launched, refused)
	}

	if err := st.Delete(root.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete("worker1"); err != nil {
		t.Fatal(err)
	}
	next := sample("extra", "")
	next.SpawnedBy = "worker2"
	if err := st.LaunchAgentSession(next, "", false, func() error { return nil }); !errors.Is(err, ErrFanoutLimit) {
		t.Fatalf("launch after pruning the root: %v, want the same limit", err)
	}
}
