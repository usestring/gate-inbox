package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func taskIDs(tasks []Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func TestDeleteTaskRefusesAnotherSessionsClaimUnlessForced(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	createTask(t, st, "held")
	if ok, err := st.ClaimTask("held", "worker01", now); err != nil || !ok {
		t.Fatalf("ClaimTask = %t, %v", ok, err)
	}

	_, found, err := st.DeleteTask("held", DeleteGuard{CallerID: "lead0001"})
	var refused *DeleteRefusedError
	if !found || !errors.As(err, &refused) || refused.HeldBy != "worker01" || len(refused.Dependents) != 0 {
		t.Fatalf("delete of a held task = found %t, %v, want refused naming worker01", found, err)
	}
	if task, err := st.Task("held"); err != nil || task.State != TaskInProgress || task.Owner != "worker01" {
		t.Fatalf("the refused delete changed the task: %+v, %v", task, err)
	}

	// The holder dropping its own claim needs no force.
	deleted, found, err := st.DeleteTask("held", DeleteGuard{CallerID: "worker01"})
	if err != nil || !found || deleted.Task.ID != "held" {
		t.Fatalf("holder's delete = %+v, %t, %v", deleted, found, err)
	}

	createTask(t, st, "held-again")
	if ok, err := st.ClaimTask("held-again", "worker01", now); err != nil || !ok {
		t.Fatalf("ClaimTask = %t, %v", ok, err)
	}
	if _, found, err := st.DeleteTask("held-again", DeleteGuard{CallerID: "lead0001", Force: true}); err != nil || !found {
		t.Fatalf("forced delete = %t, %v", found, err)
	}
}

func TestDeleteTaskRefusesAPrerequisiteAndAForcedOneUnblocksItsDependents(t *testing.T) {
	st := newTestStore(t)
	createTask(t, st, "schema")
	createTask(t, st, "docs")
	createTask(t, st, "backfill", "schema")
	createTask(t, st, "chart", "schema", "docs")

	_, _, err := st.DeleteTask("schema", DeleteGuard{CallerID: "lead0001"})
	var refused *DeleteRefusedError
	if !errors.As(err, &refused) || refused.HeldBy != "" || !slices.Equal(taskIDs(refused.Dependents), []string{"backfill", "chart"}) {
		t.Fatalf("delete of a prerequisite = %v, want refused naming backfill and chart", err)
	}
	if edges := edgeCount(t, st); edges != 3 {
		t.Fatalf("the refused delete left %d edges, want all 3", edges)
	}

	deleted, found, err := st.DeleteTask("schema", DeleteGuard{CallerID: "lead0001", Force: true})
	if err != nil || !found {
		t.Fatalf("forced delete = %t, %v", found, err)
	}
	// chart still waits on docs, so only backfill is free.
	if got := taskIDs(deleted.Unblocked); !slices.Equal(got, []string{"backfill"}) {
		t.Fatalf("unblocked = %v, want [backfill]", got)
	}
	chart, err := st.Task("chart")
	if err != nil || !slices.Equal(chart.DependsOn, []string{"docs"}) {
		t.Fatalf("chart after the delete = %+v, %v; want it waiting on docs alone", chart, err)
	}
}

// A finished task blocks nobody, so its dependents are no reason to keep
// it: removing an edge to done work changes no one's state.
func TestDeleteTaskTakesAFinishedPrerequisiteWithoutForce(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	createTask(t, st, "schema")
	createTask(t, st, "backfill", "schema")
	if ok, err := st.ClaimTask("schema", "worker01", now); err != nil || !ok {
		t.Fatalf("ClaimTask = %t, %v", ok, err)
	}
	if ok, err := st.FinishTask("schema", "worker01", now); err != nil || !ok {
		t.Fatalf("FinishTask = %t, %v", ok, err)
	}
	deleted, found, err := st.DeleteTask("schema", DeleteGuard{CallerID: "lead0001"})
	if err != nil || !found || len(deleted.Unblocked) != 0 {
		t.Fatalf("delete of finished work = %+v, %t, %v", deleted, found, err)
	}
}

// A claim racing a delete of the same task must leave exactly one of them
// standing: a delete that won leaves nothing to claim, and a claim that won
// is another session's work the delete must refuse.
func TestRacingClaimAndDeleteNeverBothTakeEffect(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "state.db")
	for round := range 20 {
		claimer, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		deleter, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		id := fmt.Sprintf("task-%d", round)
		createTask(t, claimer, id)
		start := make(chan struct{})
		var running sync.WaitGroup
		var claimed, deletedOK bool
		var claimErr, deleteErr error
		running.Add(2)
		go func() {
			defer running.Done()
			<-start
			claimed, claimErr = claimer.ClaimTask(id, "worker01", time.Now())
		}()
		go func() {
			defer running.Done()
			<-start
			_, deletedOK, deleteErr = deleter.DeleteTask(id, DeleteGuard{CallerID: "lead0001"})
		}()
		close(start)
		running.Wait()
		var refused *DeleteRefusedError
		if deleteErr != nil && !errors.As(deleteErr, &refused) {
			t.Fatalf("round %d: delete failed outright: %v", round, deleteErr)
		}
		if claimErr != nil {
			t.Fatalf("round %d: claim failed outright: %v", round, claimErr)
		}
		deleted := deletedOK && deleteErr == nil
		if claimed == deleted {
			t.Fatalf("round %d: claimed %t, deleted %t; want exactly one", round, claimed, deleted)
		}
		claimer.Close()
		deleter.Close()
	}
}

func TestPruneTasksSweepsOnlyOldPendingWorkNobodyIsLeftToWant(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	old := now.AddDate(0, 0, -30)
	live, filed := sample("live0001", ""), sample("filed001", "")
	filed.Archived = true
	for _, sess := range []Session{live, filed} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	add := func(id, creator string, created time.Time, dependsOn ...string) {
		t.Helper()
		if err := st.CreateTask(Task{ID: id, Title: "work on " + id, State: TaskPending, CreatedBy: creator,
			DependsOn: dependsOn, CreatedAt: created, UpdatedAt: created}); err != nil {
			t.Fatalf("CreateTask(%s): %v", id, err)
		}
	}
	add("a-archived", filed.ID, old)
	add("b-deleted", "gone0001", old.Add(time.Minute))
	add("c-unrecorded", "", old.Add(2*time.Minute))
	add("d-live", live.ID, old)
	add("e-recent", filed.ID, now)
	add("f-claimed", filed.ID, old)
	add("g-kept", filed.ID, old)
	add("h-building", live.ID, now, "g-kept")
	add("i-waiting", live.ID, now, "a-archived")
	if ok, err := st.ClaimTask("f-claimed", "worker01", now); err != nil || !ok {
		t.Fatalf("ClaimTask = %t, %v", ok, err)
	}
	// A dependent cannot be claimed while what it waits on is pending, so
	// one already in progress is written directly: a list from before the
	// guard can hold it.
	if _, err := st.db.Exec(`UPDATE tasks SET state = ?, owner_session_id = ? WHERE id = ?`, TaskInProgress, "worker02", "h-building"); err != nil {
		t.Fatalf("mark h-building in progress: %v", err)
	}
	cutoff := now.AddDate(0, 0, -14)
	want := []string{"a-archived", "b-deleted", "c-unrecorded"}

	dry, err := st.PruneTasks(cutoff, true)
	if err != nil {
		t.Fatalf("dry PruneTasks: %v", err)
	}
	if !slices.Equal(taskIDs(dry.Tasks), want) || !slices.Equal(taskIDs(dry.Kept), []string{"g-kept"}) || len(dry.Unblocked) != 0 {
		t.Fatalf("dry run = tasks %v kept %v unblocked %v", taskIDs(dry.Tasks), taskIDs(dry.Kept), taskIDs(dry.Unblocked))
	}
	if all, err := st.Tasks(); err != nil || len(all) != 9 {
		t.Fatalf("the dry run deleted something: %d tasks, %v", len(all), err)
	}

	pruned, err := st.PruneTasks(cutoff, false)
	if err != nil {
		t.Fatalf("PruneTasks: %v", err)
	}
	if !slices.Equal(taskIDs(pruned.Tasks), want) || !slices.Equal(taskIDs(pruned.Kept), []string{"g-kept"}) {
		t.Fatalf("prune = tasks %v kept %v", taskIDs(pruned.Tasks), taskIDs(pruned.Kept))
	}
	if !slices.Equal(taskIDs(pruned.Unblocked), []string{"i-waiting"}) {
		t.Fatalf("unblocked = %v, want [i-waiting]", taskIDs(pruned.Unblocked))
	}
	left, err := st.Tasks()
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if got := taskIDs(left); !slices.Equal(got, []string{"d-live", "f-claimed", "g-kept", "e-recent", "h-building", "i-waiting"}) {
		t.Fatalf("left = %v", got)
	}
	if edges := edgeCount(t, st); edges != 1 {
		t.Fatalf("edges after the prune = %d, want only h-building's", edges)
	}
}
