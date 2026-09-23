package store

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestMigrationPairsStayAdjacentAndMoveFromEitherMember(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"before", "source", "after"} {
		if err := st.CreateSession(sample(id, "work")); err != nil {
			t.Fatal(err)
		}
	}
	moved := sample("replacement", "stale-group")
	moved.MigrationID = "source"
	if err := st.CreateSession(moved); err != nil {
		t.Fatal(err)
	}
	assertOrder := func(want ...string) {
		t.Helper()
		sessions, err := st.ListSessions(true)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, sess := range sessions {
			ids = append(ids, sess.ID)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("order = %v, want %v", ids, want)
		}
	}
	assertOrder("before", "source", "replacement", "after")
	if changed, err := st.ReorderSession("replacement", -1, false); err != nil || !changed {
		t.Fatalf("reorder: %v %v", changed, err)
	}
	assertOrder("source", "replacement", "before", "after")
	if err := st.SwapSessionOrder("source", "after"); err != nil {
		t.Fatal(err)
	}
	assertOrder("after", "before", "source", "replacement")
	if err := st.MoveSession("replacement", "new-group"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"source", "replacement"} {
		sess, err := st.Get(id)
		if err != nil || sess.Group != "new-group" || sess.MigrationID != "source" {
			t.Fatalf("moved = %+v, %v", sess, err)
		}
	}
	if err := st.MoveSession("source", "work"); err != nil {
		t.Fatal(err)
	}
	assertOrder("after", "before", "source", "replacement")
}

func TestRepeatedMigrationKeepsOneChainAndRejectsNestingIntoItself(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("a", "work")); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"b", "a"}, {"c", "b"}} {
		sess := sample(pair[0], "work")
		sess.MigrationID = pair[1]
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PlaceSession("a", "work", "b"); err == nil {
		t.Fatal("nested a migration chain into itself")
	}
	for _, id := range []string{"a", "b", "c"} {
		sess, err := st.Get(id)
		if err != nil || sess.ParentID != "" || sess.MigrationID != "a" {
			t.Fatalf("chain member = %+v, %v", sess, err)
		}
	}
	if err := st.SetArchived("b", true); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveSession("c", "moved"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		sess, err := st.Get(id)
		if err != nil || sess.Group != "moved" {
			t.Fatalf("chain member = %+v, %v", sess, err)
		}
	}
}

func TestDeletingMigrationMemberUnlinksTheSurvivor(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("source", "work")); err != nil {
		t.Fatal(err)
	}
	moved := sample("replacement", "work")
	moved.MigrationID = "source"
	if err := st.CreateSession(moved); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete("source"); err != nil {
		t.Fatal(err)
	}
	kept, err := st.Get("replacement")
	if err != nil || kept.MigrationID != "" {
		t.Fatalf("survivor = %+v, %v", kept, err)
	}
	if err := st.MoveSession("replacement", "elsewhere"); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationLinkSurvivesReopeningTheStore(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "migration.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(sample("source", "work")); err != nil {
		st.Close()
		t.Fatal(err)
	}
	sess := sample("replacement", "work")
	sess.MigrationID = "source"
	if err := st.CreateSession(sess); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.MoveSession("replacement", "new-group"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"source", "replacement"} {
		sess, err := st.Get(id)
		if err != nil || sess.MigrationID != "source" || sess.Group != "new-group" {
			t.Fatalf("reopened member = %+v, %v", sess, err)
		}
	}
}

func TestMigrationOpeningSurvivesAnotherMigrationAndSourceDeletion(t *testing.T) {
	st := newTestStore(t)
	source := sample("original", "work")
	source.LaunchPrompt = "Add cursor pagination and test page boundaries."
	if err := st.CreateSession(source); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"second", "original"}, {"third", "second"}} {
		next := sample(pair[0], "work")
		next.MigrationID = pair[1]
		next.LaunchPrompt = "Read the transcript and continue the work."
		next.PendingInputs = []string{"deferred launch prompt"}
		if err := st.CreateSession(next); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetArchived("second", true); err != nil {
		t.Fatal(err)
	}
	if err := st.MoveSession("third", "review"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"original", "second"} {
		if err := st.Delete(id); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := st.Get("third")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kept.MigrationOpening, []string{source.LaunchPrompt}) {
		t.Fatalf("opening = %q", kept.MigrationOpening)
	}
	if kept.LaunchPrompt != "Read the transcript and continue the work." || !reflect.DeepEqual(kept.PendingInputs, []string{"deferred launch prompt"}) {
		t.Fatalf("launch state changed: %+v", kept)
	}
	if kept.MigrationID != "" || kept.Archived || kept.Group != "review" {
		t.Fatalf("lifecycle state = %+v", kept)
	}
}
