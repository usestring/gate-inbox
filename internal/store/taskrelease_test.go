package store

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

// claimFor creates a task and has sessionID claim it, failing the test if
// the claim does not land.
func claimFor(t *testing.T, st *Store, taskID, sessionID string) {
	t.Helper()
	createTask(t, st, taskID)
	claimed, err := st.ClaimTask(taskID, sessionID, time.Now())
	if err != nil || !claimed {
		t.Fatalf("ClaimTask(%s, %s) = %t, %v", taskID, sessionID, claimed, err)
	}
}

func requireTask(t *testing.T, st *Store, id, state, owner string) {
	t.Helper()
	task, err := st.Task(id)
	if err != nil {
		t.Fatalf("Task(%s): %v", id, err)
	}
	if task.State != state || task.Owner != owner {
		t.Fatalf("task %s = state %q owner %q, want state %q owner %q", id, task.State, task.Owner, state, owner)
	}
}

// An archived agent is gone for good as far as the list is concerned, so its
// claims go back with the archive, not with the row's deletion a week later.
// A finished task and another session's claim are left alone.
func TestArchiveSessionReleasesItsInProgressClaims(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"worker", "other"} {
		if err := st.CreateSession(sample(id, "g")); err != nil {
			t.Fatalf("CreateSession(%s): %v", id, err)
		}
	}
	claimFor(t, st, "a", "worker")
	claimFor(t, st, "b", "worker")
	claimFor(t, st, "done", "worker")
	if _, err := st.FinishTask("done", "worker", time.Now()); err != nil {
		t.Fatalf("FinishTask: %v", err)
	}
	claimFor(t, st, "theirs", "other")

	released, err := st.ArchiveSession("worker")
	if err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if released != 2 {
		t.Fatalf("released = %d, want 2", released)
	}
	requireTask(t, st, "a", TaskPending, "")
	requireTask(t, st, "b", TaskPending, "")
	requireTask(t, st, "done", TaskDone, "worker")
	requireTask(t, st, "theirs", TaskInProgress, "other")

	// Restoring takes nothing back: the revived agent claims again.
	if err := st.SetArchived("worker", false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	requireTask(t, st, "a", TaskPending, "")
}

func TestSetArchivedReleasesClaimsForEveryCaller(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("worker", "g")); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	claimFor(t, st, "a", "worker")
	if err := st.SetArchived("worker", true); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}
	requireTask(t, st, "a", TaskPending, "")
}

// A row that is already gone fails the archive, and the failed archive must
// not have handed anything back.
func TestArchiveSessionOfAMissingRowReleasesNothing(t *testing.T) {
	st := newTestStore(t)
	createTask(t, st, "a")
	if _, err := st.db.Exec(`UPDATE tasks SET state = ?, owner_session_id = 'ghost' WHERE id = 'a'`, TaskInProgress); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := st.ArchiveSession("ghost"); err == nil {
		t.Fatal("archiving a missing row succeeded")
	}
	requireTask(t, st, "a", TaskInProgress, "ghost")
}

func TestMarkKilledReleasesClaimsAndReadsDead(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("worker", "g")); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	claimFor(t, st, "a", "worker")
	released, err := st.MarkKilled("worker")
	if err != nil || released != 1 {
		t.Fatalf("MarkKilled = %d, %v; want 1", released, err)
	}
	requireTask(t, st, "a", TaskPending, "")
	sess, err := st.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sess.Status != status.Dead {
		t.Fatalf("status = %q, want dead", sess.Status)
	}
}

// A status write alone -- a restart's, a park's, a pane that exited -- keeps
// the claims: the session may come back on the same conversation.
func TestUpdateStatusDeadKeepsClaims(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("worker", "g")); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	claimFor(t, st, "a", "worker")
	if err := st.UpdateStatus("worker", status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	requireTask(t, st, "a", TaskInProgress, "worker")
}

// The board's archive and the child sweep's auto-archive go through
// ArchiveKilled; a row that went missing keeps its claims, as it was not
// filed by this call.
func TestArchiveKilledReleasesClaimsOfEveryRowFiled(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"killed", "rested"} {
		if err := st.CreateSession(sample(id, "g")); err != nil {
			t.Fatalf("CreateSession(%s): %v", id, err)
		}
	}
	claimFor(t, st, "a", "killed")
	claimFor(t, st, "b", "rested")
	createTask(t, st, "c")
	if _, err := st.db.Exec(`UPDATE tasks SET state = ?, owner_session_id = 'ghost' WHERE id = 'c'`, TaskInProgress); err != nil {
		t.Fatalf("seed: %v", err)
	}
	missing, err := st.ArchiveKilled([]string{"killed", "rested", "ghost"}, []string{"killed"})
	if err != nil {
		t.Fatalf("ArchiveKilled: %v", err)
	}
	if len(missing) != 1 || missing[0] != "ghost" {
		t.Fatalf("missing = %v, want [ghost]", missing)
	}
	requireTask(t, st, "a", TaskPending, "")
	requireTask(t, st, "b", TaskPending, "")
	requireTask(t, st, "c", TaskInProgress, "ghost")
}

func TestArchiveStoppedLaunchReleasesClaims(t *testing.T) {
	st := newTestStore(t)
	sess := sample("worker", "g")
	sess.Status = status.Dead
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	stored, err := st.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	claimFor(t, st, "a", "worker")
	archived, err := st.ArchiveStoppedLaunch(stored)
	if err != nil || !archived {
		t.Fatalf("ArchiveStoppedLaunch = %t, %v", archived, err)
	}
	requireTask(t, st, "a", TaskPending, "")
}

// A stopped launch that was relaunched since is not archived, and keeps
// what it claimed on the new launch.
func TestArchiveStoppedLaunchThatDidNotMatchKeepsClaims(t *testing.T) {
	st := newTestStore(t)
	sess := sample("worker", "g")
	sess.Status = status.Working
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	stored, err := st.Get("worker")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	claimFor(t, st, "a", "worker")
	archived, err := st.ArchiveStoppedLaunch(stored)
	if err != nil || archived {
		t.Fatalf("ArchiveStoppedLaunch = %t, %v; want false", archived, err)
	}
	requireTask(t, st, "a", TaskInProgress, "worker")
}

func TestGroupArchiveReleasesClaimsOfEverySessionInIt(t *testing.T) {
	st := newTestStore(t)
	for id, group := range map[string]string{"in": "proj", "nested": "proj/sub", "outside": "proj2"} {
		if err := st.CreateSession(sample(id, group)); err != nil {
			t.Fatalf("CreateSession(%s): %v", id, err)
		}
	}
	claimFor(t, st, "a", "in")
	claimFor(t, st, "b", "nested")
	claimFor(t, st, "c", "outside")
	if err := st.SetGroupArchived("proj", true); err != nil {
		t.Fatalf("SetGroupArchived: %v", err)
	}
	requireTask(t, st, "a", TaskPending, "")
	requireTask(t, st, "b", TaskPending, "")
	requireTask(t, st, "c", TaskInProgress, "outside")
}

// A migration carries the work on under a new id, so the claims follow it.
func TestCompleteMigrationMovesClaimsToTheNewSession(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"from", "to"} {
		if err := st.CreateSession(sample(id, "g")); err != nil {
			t.Fatalf("CreateSession(%s): %v", id, err)
		}
	}
	claimFor(t, st, "a", "from")
	if err := st.CompleteMigration("from", "to"); err != nil {
		t.Fatalf("CompleteMigration: %v", err)
	}
	requireTask(t, st, "a", TaskInProgress, "to")
}
