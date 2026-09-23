package store

import (
	"strings"
	"testing"
	"time"
)

// childOf is a child row in a given state, aged so the window tests have
// something to measure against.
func childOf(t *testing.T, st *Store, parentID, id, state string, age time.Duration) {
	t.Helper()
	sess := sample(id, "")
	sess.ParentID = parentID
	sess.Status = state
	sess.LastStatusAt = time.Now().Add(-age)
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession(%s): %v", id, err)
	}
}

func archivable(t *testing.T, st *Store, window time.Duration) string {
	t.Helper()
	children, err := st.AutoArchivableChildren(time.Now().Add(-window))
	if err != nil {
		t.Fatalf("AutoArchivableChildren: %v", err)
	}
	ids := make([]string, 0, len(children))
	for _, child := range children {
		ids = append(ids, child.ID)
	}
	return strings.Join(ids, ",")
}

func TestAutoArchivableChildrenTakesADeadChildPastTheWindow(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "old", "dead", time.Hour)
	childOf(t, st, "p1", "fresh", "dead", time.Minute)
	if got := archivable(t, st, 30*time.Minute); got != "old" {
		t.Fatalf("archivable = %q, want the child dead past the window only", got)
	}
}

// A finished child still has an agent in its pane. However long it rests
// there, the sweep is not what ends it: on 2026-09-11 it ended eight working
// children of one parent this way, seconds after each came to rest.
func TestAutoArchivableChildrenLeavesAFinishedChildWhateverTheWindow(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "c1", "finished", 3*time.Hour)
	id, _, err := st.Enqueue(InboxMessage{
		SessionID: "p1", SenderID: "c1", SenderName: "probe",
		Body: "interim: halfway there", SentAt: time.Now(),
	}, DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := st.MarkDelivered(id, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if got := archivable(t, st, 30*time.Minute); got != "" {
		t.Fatalf("archivable = %q, want nothing: a finished child is still in its pane", got)
	}
}

// An exited child that reported back is finished business the moment the
// report lands, whatever the window says.
func TestAutoArchivableChildrenTakesAChildThatReportedBack(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "c1", "dead", time.Minute)
	id, _, err := st.Enqueue(InboxMessage{
		SessionID:  "p1",
		SenderID:   "c1",
		SenderName: "probe",
		Body:       "done: the answer is 42",
		SentAt:     time.Now(),
	}, DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := archivable(t, st, 30*time.Minute); got != "" {
		t.Fatalf("archivable = %q, want nothing while the report is still queued", got)
	}
	if err := st.MarkDelivered(id, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if got := archivable(t, st, 30*time.Minute); got != "c1" {
		t.Fatalf("archivable = %q, want the child whose report was delivered", got)
	}
	children, err := st.AutoArchivableChildren(time.Now().Add(-30 * time.Minute))
	if err != nil {
		t.Fatalf("AutoArchivableChildren: %v", err)
	}
	if len(children) != 1 || children[0].Reason != ArchiveReasonReported {
		t.Fatalf("reason = %+v, want the delivered report named", children)
	}
}

func TestAutoArchivableChildrenLeavesLiveAndUnparentedRows(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "working", "working", time.Hour)
	childOf(t, st, "p1", "waiting", "waiting", time.Hour)
	childOf(t, st, "p1", "errored", "errored", time.Hour)
	childOf(t, st, "p1", "idle", "idle", time.Hour)
	// A root session is nobody's child and is never swept, however long it
	// has been sitting there.
	root := sample("lonely", "")
	root.Status = "finished"
	root.LastStatusAt = time.Now().Add(-time.Hour)
	if err := st.CreateSession(root); err != nil {
		t.Fatalf("root: %v", err)
	}
	if got := archivable(t, st, 30*time.Minute); got != "" {
		t.Fatalf("archivable = %q, want nothing: no exited child, and a root is not a child", got)
	}
}

func TestAutoArchivableChildrenTakesADeadChild(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "gone", "dead", time.Hour)
	if got := archivable(t, st, 30*time.Minute); got != "gone" {
		t.Fatalf("archivable = %q, want the dead child", got)
	}
}

// Sweeping the same child twice would fight the archive: once filed, it is
// out of the query.
func TestAutoArchivableChildrenSkipsWhatIsAlreadyArchived(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "c1", "dead", time.Hour)
	if err := st.SetArchived("c1", true); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}
	if got := archivable(t, st, 30*time.Minute); got != "" {
		t.Fatalf("archivable = %q, want nothing: it is already filed", got)
	}
}

func TestArchivedChildCountsCountsPerParent(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"p1", "p2"} {
		if err := st.CreateSession(sample(id, "")); err != nil {
			t.Fatalf("parent %s: %v", id, err)
		}
	}
	childOf(t, st, "p1", "a", "finished", time.Hour)
	childOf(t, st, "p1", "b", "finished", time.Hour)
	childOf(t, st, "p1", "live", "working", time.Hour)
	childOf(t, st, "p2", "c", "finished", time.Hour)
	for _, id := range []string{"a", "b", "c"} {
		if err := st.SetArchived(id, true); err != nil {
			t.Fatalf("SetArchived(%s): %v", id, err)
		}
	}
	counts, err := st.ArchivedChildCounts()
	if err != nil {
		t.Fatalf("ArchivedChildCounts: %v", err)
	}
	if counts["p1"] != 2 || counts["p2"] != 1 {
		t.Fatalf("counts = %v, want p1:2 p2:1", counts)
	}
}

// An acked child is idle with an agent still in its pane. A person looking
// at it is not the pane exiting, so the sweep leaves it.
func TestAutoArchivableChildrenLeavesAnAckedChild(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "c1", "finished", time.Hour)
	if err := st.AcknowledgeFinished("c1"); err != nil {
		t.Fatalf("AcknowledgeFinished: %v", err)
	}
	if got := archivable(t, st, -time.Second); got != "" {
		t.Fatalf("archivable = %q, want nothing: an acked child is still in its pane", got)
	}
}

// An idle child nobody acked is still working its way through something.
func TestAutoArchivableChildrenLeavesAnUnackedIdleChild(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "c1", "idle", time.Hour)
	if got := archivable(t, st, -time.Second); got != "" {
		t.Fatalf("archivable = %q, want nothing: idle without an ack is not done", got)
	}
}

// The count has to outlive the rows: the retention sweep deletes an archived
// child after seven days, and a count derived from the sessions table would
// shrink under the badge as it went.
func TestArchivedChildCountsSurviveTheRowsBeingDeleted(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "c1", "dead", time.Hour)
	if err := st.SetArchived("c1", true); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}
	if counts, _ := st.ArchivedChildCounts(); counts["p1"] != 1 {
		t.Fatalf("counts = %v, want p1:1", counts)
	}
	if err := st.Delete("c1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	counts, err := st.ArchivedChildCounts()
	if err != nil {
		t.Fatalf("ArchivedChildCounts: %v", err)
	}
	if counts["p1"] != 1 {
		t.Fatalf("counts = %v after the row was deleted, want p1:1", counts)
	}
}

// Restoring a child takes it back out of the tally, or the badge would count
// a session that is on the list again.
func TestRestoringAChildTakesItOutOfTheTally(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "c1", "dead", time.Hour)
	if err := st.SetArchived("c1", true); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}
	if err := st.SetArchived("c1", false); err != nil {
		t.Fatalf("SetArchived restore: %v", err)
	}
	if counts, _ := st.ArchivedChildCounts(); counts["p1"] != 0 {
		t.Fatalf("counts = %v, want p1:0 after the restore", counts)
	}
}

// A terminal is a leaf, so it may hang off a session that is itself a child.
// Refusing that broke create_terminal from every agent a worker spawned.
func TestCreateSessionLeafNestsUnderAChild(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	child := sample("c1", "")
	child.ParentID = "p1"
	if err := st.CreateSession(child); err != nil {
		t.Fatalf("child: %v", err)
	}
	shell := sample("sh1", "")
	shell.ParentID = "c1"
	if err := st.CreateSession(shell); err == nil {
		t.Fatal("an ordinary session nested two deep, which the depth cap should refuse")
	}
	leaf := sample("sh2", "")
	leaf.ParentID = "c1"
	if err := st.CreateSessionLeaf(leaf); err != nil {
		t.Fatalf("CreateSessionLeaf under a child: %v", err)
	}
	stored, err := st.Get("sh2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.ParentID != "c1" {
		t.Fatalf("leaf parent = %q, want c1", stored.ParentID)
	}
}

// A revived child's earlier life left its reports in the parent's inbox,
// delivered. They are not the new life's report: once this life has exited
// too, it is archivable only on a report the agent that was in the pane sent.
func TestAutoArchivableChildrenIgnoresAReportFromBeforeTheLaunch(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatalf("parent: %v", err)
	}
	childOf(t, st, "p1", "c1", "dead", time.Minute)
	stale, _, err := st.Enqueue(InboxMessage{
		SessionID: "p1", SenderID: "c1", SenderName: "probe",
		Body: "done: first life", SentAt: time.Now().Add(-time.Hour),
	}, DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := st.MarkDelivered(stale, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if err := st.SetAgentLaunchedAt("c1", time.Now().Add(-10*time.Minute)); err != nil {
		t.Fatalf("SetAgentLaunchedAt: %v", err)
	}
	if got := archivable(t, st, 30*time.Minute); got != "" {
		t.Fatalf("archivable = %q, want nothing on a report the previous life sent", got)
	}
	fresh, _, err := st.Enqueue(InboxMessage{
		SessionID: "p1", SenderID: "c1", SenderName: "probe",
		Body: "done: second life", SentAt: time.Now(),
	}, DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := st.MarkDelivered(fresh, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if got := archivable(t, st, 30*time.Minute); got != "c1" {
		t.Fatalf("archivable = %q, want the child once this life reported", got)
	}
}
