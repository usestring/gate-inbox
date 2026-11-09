package store

import (
	"slices"
	"testing"
	"time"
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
