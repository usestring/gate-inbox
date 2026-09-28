package store

import (
	"strings"
	"testing"
	"time"
)

func retirable(t *testing.T, st *Store, grace time.Duration) string {
	t.Helper()
	children, err := st.RetirableChildren(time.Now().Add(-grace))
	if err != nil {
		t.Fatalf("RetirableChildren: %v", err)
	}
	ids := make([]string, 0, len(children))
	for _, child := range children {
		ids = append(ids, child.ID)
	}
	return strings.Join(ids, ",")
}

// restNotice is the board's rest relay from child to parent, delivered at
// deliveredAt when that is not zero.
func restNotice(t *testing.T, st *Store, parentID, childID string, sentAt, deliveredAt time.Time) {
	t.Helper()
	id, _, err := st.Enqueue(InboxMessage{
		SessionID: parentID, SenderID: childID, SenderName: "n-" + childID,
		Body:    childID + " has finished its turn.",
		Subject: ChildRestSubject(childID), SentAt: sentAt,
	}, DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !deliveredAt.IsZero() {
		if err := st.MarkDelivered(id, deliveredAt); err != nil {
			t.Fatalf("MarkDelivered: %v", err)
		}
	}
}

func withParent(t *testing.T) *Store {
	t.Helper()
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	return st
}

func TestRetirableChildrenTakesAFinishWhoseNoticeWasDeliveredPastTheGrace(t *testing.T) {
	st := withParent(t)
	now := time.Now()
	childOf(t, st, "p1", "old", "finished", time.Hour)
	restNotice(t, st, "p1", "old", now.Add(-59*time.Minute), now.Add(-30*time.Minute))
	childOf(t, st, "p1", "fresh", "finished", time.Hour)
	restNotice(t, st, "p1", "fresh", now.Add(-59*time.Minute), now.Add(-time.Minute))
	if got := retirable(t, st, 10*time.Minute); got != "old" {
		t.Fatalf("retirable = %q, want only the child absorbed past the grace", got)
	}
}

// Finished on the screen is not taken in. A child whose rest notice is still
// queued, or was never sent, stays however long it rests: the parent has not
// heard, and ending it is the 2026-09-11 incident again.
func TestRetirableChildrenLeavesAFinishNobodyTookIn(t *testing.T) {
	st := withParent(t)
	now := time.Now()
	childOf(t, st, "p1", "queued", "finished", 3*time.Hour)
	restNotice(t, st, "p1", "queued", now.Add(-3*time.Hour), time.Time{})
	childOf(t, st, "p1", "silent", "finished", 3*time.Hour)
	if got := retirable(t, st, 10*time.Minute); got != "" {
		t.Fatalf("retirable = %q, want nothing the parent has not heard of", got)
	}
}

// A notice from an earlier turn says nothing about this one.
func TestRetirableChildrenIgnoresANoticeOlderThanTheCurrentRest(t *testing.T) {
	st := withParent(t)
	now := time.Now()
	childOf(t, st, "p1", "c1", "finished", 20*time.Minute)
	restNotice(t, st, "p1", "c1", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	if got := retirable(t, st, 10*time.Minute); got != "" {
		t.Fatalf("retirable = %q, want nothing: the delivered notice is from an earlier turn", got)
	}
}

func TestRetirableChildrenTakesAFinishTheSpawnerRead(t *testing.T) {
	st := withParent(t)
	childOf(t, st, "p1", "c1", "finished", time.Hour)
	if err := st.NoteSpawnerRead("c1", time.Now().Add(-20*time.Minute)); err != nil {
		t.Fatalf("NoteSpawnerRead: %v", err)
	}
	children, err := st.RetirableChildren(time.Now().Add(-10 * time.Minute))
	if err != nil {
		t.Fatalf("RetirableChildren: %v", err)
	}
	if len(children) != 1 || children[0].ID != "c1" || children[0].Via != AbsorbedByRead {
		t.Fatalf("retirable = %+v, want c1 absorbed by the read", children)
	}
	if got := retirable(t, st, 30*time.Minute); got != "" {
		t.Fatalf("retirable = %q, want nothing inside a grace longer than the read's age", got)
	}
}

// A read of a child mid-turn is progress, not the finish being taken in.
func TestNoteSpawnerReadStampsOnlyAFinishedChild(t *testing.T) {
	st := withParent(t)
	childOf(t, st, "p1", "c1", "working", time.Hour)
	if err := st.NoteSpawnerRead("c1", time.Now().Add(-20*time.Minute)); err != nil {
		t.Fatalf("NoteSpawnerRead: %v", err)
	}
	if err := st.UpdateStatus("c1", "finished"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if got := retirable(t, st, 0); got != "" {
		t.Fatalf("retirable = %q, want nothing: the only read came while it worked", got)
	}
}

func TestRetirableChildrenLeavesEveryStateButFinished(t *testing.T) {
	st := withParent(t)
	now := time.Now()
	for _, state := range []string{"waiting", "working", "starting", "errored", "idle", "dead"} {
		childOf(t, st, "p1", state, state, time.Hour)
		restNotice(t, st, "p1", state, now.Add(-59*time.Minute), now.Add(-59*time.Minute))
		if err := st.NoteSpawnerRead(state, now.Add(-59*time.Minute)); err != nil {
			t.Fatalf("NoteSpawnerRead: %v", err)
		}
	}
	if got := retirable(t, st, 10*time.Minute); got != "" {
		t.Fatalf("retirable = %q, want nothing that is not finished", got)
	}
}

func TestRetirableChildrenHonoursKeep(t *testing.T) {
	st := withParent(t)
	now := time.Now()
	childOf(t, st, "p1", "c1", "finished", time.Hour)
	restNotice(t, st, "p1", "c1", now.Add(-59*time.Minute), now.Add(-59*time.Minute))
	if err := st.SetKeepChild("c1", true); err != nil {
		t.Fatalf("SetKeepChild: %v", err)
	}
	if keep, err := st.KeepChild("c1"); err != nil || !keep {
		t.Fatalf("KeepChild = %v, %v", keep, err)
	}
	if got := retirable(t, st, 10*time.Minute); got != "" {
		t.Fatalf("retirable = %q, want the kept child left", got)
	}
	if err := st.SetKeepChild("c1", false); err != nil {
		t.Fatalf("SetKeepChild: %v", err)
	}
	if got := retirable(t, st, 10*time.Minute); got != "c1" {
		t.Fatalf("retirable = %q, want c1 once the keep is cleared", got)
	}
	if err := st.SetKeepChild("nope", true); err == nil {
		t.Fatal("SetKeepChild on a missing row succeeded")
	}
}

// The grace window exists for a follow-up, so one queued for the child and
// not yet read holds it.
func TestRetirableChildrenLeavesAChildWithAFollowUpQueued(t *testing.T) {
	st := withParent(t)
	now := time.Now()
	childOf(t, st, "p1", "c1", "finished", time.Hour)
	restNotice(t, st, "p1", "c1", now.Add(-59*time.Minute), now.Add(-59*time.Minute))
	if _, _, err := st.Enqueue(InboxMessage{
		SessionID: "c1", SenderID: "p1", SenderName: "n-p1",
		Body: "one more thing", SentAt: now,
	}, DefaultInboxLimits); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := retirable(t, st, 10*time.Minute); got != "" {
		t.Fatalf("retirable = %q, want the child with a message waiting left", got)
	}
}

func TestRetirableChildrenLeavesAnAdoptedPane(t *testing.T) {
	st := withParent(t)
	now := time.Now()
	sess := sample("c1", "")
	sess.ParentID, sess.Status = "p1", "finished"
	sess.LastStatusAt = now.Add(-time.Hour)
	sess.TmuxSocket, sess.TmuxPaneID = "/tmp/elsewhere", "%7"
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	restNotice(t, st, "p1", "c1", now.Add(-59*time.Minute), now.Add(-59*time.Minute))
	if got := retirable(t, st, 10*time.Minute); got != "" {
		t.Fatalf("retirable = %q, want an adopted pane left to its owner", got)
	}
}

// A grandchild is filed under the root, so only spawned_by says whose it is.
func TestDescendantsFollowsTheSpawnerNotTheDrawing(t *testing.T) {
	rows := []Session{
		{ID: "root"},
		{ID: "kid", ParentID: "root", SpawnedBy: "root"},
		{ID: "grandkid", ParentID: "root", SpawnedBy: "kid"},
		{ID: "great", ParentID: "root", SpawnedBy: "grandkid"},
		{ID: "sibling", ParentID: "root", SpawnedBy: "root"},
		{ID: "legacy", ParentID: "kid"},
	}
	var ids []string
	for _, sess := range Descendants(rows, "kid") {
		ids = append(ids, sess.ID)
	}
	if got := strings.Join(ids, ","); got != "grandkid,legacy,great" {
		t.Fatalf("Descendants(kid) = %q", got)
	}
}
