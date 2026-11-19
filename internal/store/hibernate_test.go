package store

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

// A hibernated child reads dead with no pane, the exited case the child
// sweep files away. Filing it would start the archive's deletion clock on a
// conversation that was only paused, so the sweep leaves it.
func TestAutoArchivableChildrenLeavesAHibernatedChild(t *testing.T) {
	s := openRetentionStore(t)
	if err := s.CreateSession(Session{ID: "kid", Name: "kid", Tool: "claude", ParentID: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus("kid", "dead"); err != nil {
		t.Fatal(err)
	}
	ids := func() []string {
		t.Helper()
		children, err := s.AutoArchivableChildren(time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, child := range children {
			out = append(out, child.ID)
		}
		return out
	}
	if !slices.Contains(ids(), "kid") {
		t.Fatal("a dead child past the window was not archivable to begin with")
	}
	if err := s.AddHibernated([]string{"kid"}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ids(), "kid") {
		t.Fatal("the sweep would file a hibernated child")
	}
}

func TestHibernatedToSettleNamesRowsWithMailOrNoLongerDead(t *testing.T) {
	s := openRetentionStore(t)
	for _, id := range []string{"quiet", "mailed", "revived"} {
		if err := s.CreateSession(Session{ID: id, Name: id, Tool: "claude"}); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateStatus(id, "dead"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddHibernated([]string{"quiet", "mailed", "revived", "deleted"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Enqueue(InboxMessage{SessionID: "mailed", SenderID: "x", SenderName: "x", Body: "hi"}, DefaultInboxLimits); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus("revived", "idle"); err != nil {
		t.Fatal(err)
	}
	got, err := s.HibernatedToSettle()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"deleted", "mailed", "revived"}; !slices.Equal(got, want) {
		t.Fatalf("settle = %v, want %v", got, want)
	}
}

// The overlay names a hibernated row for what it is and leaves everything
// else alone: a row that really died, an archived one, and one that came
// back some other way keep their stored status.
func TestApplyHibernatedReadsOnlyLiveDeadRowsInTheSet(t *testing.T) {
	t.Parallel()
	rows := []Session{
		{ID: "sleep001", Status: status.Dead},
		{ID: "gone0001", Status: status.Dead},
		{ID: "filed001", Status: status.Dead, Archived: true},
		{ID: "back0001", Status: status.Idle},
	}
	set := []string{"sleep001", "filed001", "back0001"}
	got := ApplyHibernated(slices.Clone(rows), set)
	want := []string{status.Hibernating, status.Dead, status.Dead, status.Idle}
	for i, sess := range got {
		if sess.Status != want[i] {
			t.Fatalf("%s = %s, want %s", sess.ID, sess.Status, want[i])
		}
		if shown := Shown(rows[i], set); shown != want[i] {
			t.Fatalf("Shown(%s) = %s, want %s", sess.ID, shown, want[i])
		}
	}
}
