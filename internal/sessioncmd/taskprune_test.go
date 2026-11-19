package sessioncmd

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

func TestDeleteNamesWhatStopsItAndForceSaysWhatItFreed(t *testing.T) {
	h := newSessionHarness(t)
	worker := h.addChildRow(t, "retry-worker", h.caller.ID)
	held, err := h.sessions.CreateTask(h.caller.ID, TaskSpec{Title: "add the retry column"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	dependent, err := h.sessions.CreateTask(h.caller.ID, TaskSpec{Title: "backfill retries", DependsOn: []string{held.ID}})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := h.sessions.ClaimTask(worker, held.ID, ClaimOptions{}); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	// Both reasons at once, each with what the caller needs to act on it:
	// the holder to ask, the dependents to clear, and the override.
	_, err = h.sessions.DeleteTask(h.caller.ID, held.ID, false)
	if err == nil {
		t.Fatal("deleting another session's claim with a dependent went through")
	}
	for _, want := range []string{
		"task " + held.ID + " was not deleted",
		"held by retry-worker",
		"1 unfinished task waits on it: backfill retries (" + dependent.ID + ") [pending]",
		`task with action "delete" and force true`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q is missing %q", err, want)
		}
	}
	if task, err := h.store.Task(held.ID); err != nil || task.State != store.TaskInProgress {
		t.Fatalf("the refused delete changed the task: %+v, %v", task, err)
	}

	deleted, err := h.sessions.DeleteTask(h.caller.ID, held.ID, true)
	if err != nil {
		t.Fatalf("forced DeleteTask: %v", err)
	}
	if deleted.Task.ID != held.ID || deleted.Task.OwnerName != "retry-worker" {
		t.Fatalf("deleted = %+v, want the task as it stood", deleted.Task)
	}
	if len(deleted.Unblocked) != 1 || deleted.Unblocked[0].ID != dependent.ID || deleted.Unblocked[0].Blocked {
		t.Fatalf("unblocked = %+v, want %s claimable", deleted.Unblocked, dependent.ID)
	}
	if _, err := h.sessions.ClaimTask(worker, dependent.ID, ClaimOptions{}); err != nil {
		t.Fatalf("the unblocked dependent could not be claimed: %v", err)
	}
}

func TestPruneIsADryRunUntilAskedAndSweepsOnlyStaleWork(t *testing.T) {
	h := newSessionHarness(t)
	filed := h.addSessionRow(t, "filed-lead")
	if err := h.store.SetArchived(filed, true); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}
	old := time.Now().AddDate(0, 0, -20)
	for _, task := range []store.Task{
		{ID: "stale001", Title: "an abandoned plan", CreatedBy: filed},
		{ID: "stale002", Title: "a plan from before creators", CreatedBy: ""},
		{ID: "live0001", Title: "the caller's own plan", CreatedBy: h.caller.ID},
	} {
		task.State, task.CreatedAt, task.UpdatedAt = store.TaskPending, old, old
		if err := h.store.CreateTask(task); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}

	dry, err := h.sessions.PruneTasks(h.caller.ID, TaskPruneOptions{})
	if err != nil {
		t.Fatalf("PruneTasks: %v", err)
	}
	if !dry.DryRun || dry.OlderThanDays != DefaultPruneDays || dry.Count != 2 {
		t.Fatalf("dry run = %+v", dry)
	}
	text := FormatTaskPrune(dry)
	for _, want := range []string{"would delete 2 stale tasks", "nothing was deleted", "an abandoned plan (stale001)", "created by filed-lead"} {
		if !strings.Contains(text, want) {
			t.Errorf("dry run text %q is missing %q", text, want)
		}
	}

	// Thirty days back is past every task here, so nothing qualifies.
	if none, err := h.sessions.PruneTasks(h.caller.ID, TaskPruneOptions{OlderThanDays: 30, Delete: true}); err != nil || none.Count != 0 {
		t.Fatalf("a prune older than every task = %+v, %v", none, err)
	}
	pruned, err := h.sessions.PruneTasks(h.caller.ID, TaskPruneOptions{Delete: true})
	if err != nil || pruned.DryRun || pruned.Count != 2 {
		t.Fatalf("PruneTasks = %+v, %v", pruned, err)
	}
	listed, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(listed.Tasks) != 1 || listed.Tasks[0].ID != "live0001" {
		t.Fatalf("left after the prune = %+v", listed.Tasks)
	}

	for _, days := range []int{-1, MaxPruneDays + 1} {
		if _, err := h.sessions.PruneTasks(h.caller.ID, TaskPruneOptions{OlderThanDays: days}); err == nil ||
			!strings.Contains(err.Error(), "out of range") {
			t.Fatalf("older_than_days %d = %v", days, err)
		}
	}
}
