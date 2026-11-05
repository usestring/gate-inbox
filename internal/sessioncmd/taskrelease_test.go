package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// claimAs creates a task and has claimant claim it through the same path the
// task tool takes.
func claimAs(t *testing.T, h *sessionHarness, claimant, title string) string {
	t.Helper()
	created, err := h.sessions.CreateTask(h.caller.ID, title, "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	claimed, err := h.sessions.ClaimTask(claimant, created.ID)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed.State != store.TaskInProgress || claimed.Owner != claimant {
		t.Fatalf("claim = %+v", claimed)
	}
	return created.ID
}

func requireReleased(t *testing.T, h *sessionHarness, taskID string) {
	t.Helper()
	task, err := h.store.Task(taskID)
	if err != nil {
		t.Fatalf("Task(%s): %v", taskID, err)
	}
	if task.State != store.TaskPending || task.Owner != "" {
		t.Fatalf("task %s = state %q owner %q, want pending and unowned", taskID, task.State, task.Owner)
	}
}

func TestArchiveSessionReleasesTheClaimsOfItAndItsFanOut(t *testing.T) {
	h := newSessionHarness(t)
	worker := childRow(t, h, store.Session{ID: "a0a0a1", Name: "worker", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Working}, true)
	grand := childRow(t, h, store.Session{ID: "b0b0b2", Name: "grandchild", ParentID: h.caller.ID, SpawnedBy: worker.ID, Status: status.Working}, true)
	mine := claimAs(t, h, worker.ID, "worker's job")
	theirs := claimAs(t, h, grand.ID, "grandchild's job")

	filed, err := h.sessions.Archive(h.caller.ID, worker.ID, true)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if filed.ReleasedTasks != 2 {
		t.Fatalf("released_tasks = %d, want 2", filed.ReleasedTasks)
	}
	requireReleased(t, h, mine)
	requireReleased(t, h, theirs)
	if line := FormatArchiveState(filed); !strings.Contains(line, "released 2 in-progress tasks back to pending") {
		t.Fatalf("archive line does not say what it released: %q", line)
	}
}

func TestKillSessionReleasesItsClaims(t *testing.T) {
	h := newSessionHarness(t)
	worker := childRow(t, h, store.Session{ID: "c0c0c3", Name: "worker", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Working}, true)
	task := claimAs(t, h, worker.ID, "worker's job")

	killed, err := h.sessions.Kill(h.caller.ID, worker.ID, extension.KillByMCP)
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if killed.ReleasedTasks != 1 {
		t.Fatalf("released_tasks = %d, want 1", killed.ReleasedTasks)
	}
	requireReleased(t, h, task)
	row, err := h.store.Get(worker.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Status != status.Dead {
		t.Fatalf("status after kill = %q, want dead", row.Status)
	}
	if line := FormatKilled(killed); !strings.Contains(line, "released 1 in-progress task back to pending") {
		t.Fatalf("kill line does not say what it released: %q", line)
	}
}

func TestCleanupChildrenReleasesTheClaimsOfEveryChildFiled(t *testing.T) {
	h := newSessionHarness(t)
	done := childRow(t, h, store.Session{ID: "d0d0d4", Name: "done", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Finished}, true)
	dead := childRow(t, h, store.Session{ID: "e0e0e5", Name: "dead", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Dead}, false)
	busy := childRow(t, h, store.Session{ID: "f0f0f6", Name: "busy", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Working}, true)
	doneTask := claimAs(t, h, done.ID, "done's leftover")
	deadTask := claimAs(t, h, dead.ID, "dead's job")
	busyTask := claimAs(t, h, busy.ID, "busy's job")

	cleaned, err := h.sessions.CleanupChildren(h.caller.ID, CleanupOptions{})
	if err != nil {
		t.Fatalf("CleanupChildren: %v", err)
	}
	if cleaned.ReleasedTasks != 2 {
		t.Fatalf("released_tasks = %d, want 2", cleaned.ReleasedTasks)
	}
	requireReleased(t, h, doneTask)
	requireReleased(t, h, deadTask)
	// The working child was left on the list, so its claim stands.
	held, err := h.store.Task(busyTask)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if held.State != store.TaskInProgress || held.Owner != busy.ID {
		t.Fatalf("skipped child's task = %+v", held)
	}
	if line := FormatChildCleanup(cleaned); !strings.Contains(line, "released 2 in-progress tasks") {
		t.Fatalf("cleanup line does not say what it released: %q", line)
	}
}

// A dry run files nothing, so it hands nothing back.
func TestCleanupChildrenDryRunReleasesNothing(t *testing.T) {
	h := newSessionHarness(t)
	done := childRow(t, h, store.Session{ID: "a0b0c7", Name: "done", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Finished}, true)
	task := claimAs(t, h, done.ID, "done's leftover")

	cleaned, err := h.sessions.CleanupChildren(h.caller.ID, CleanupOptions{DryRun: true})
	if err != nil {
		t.Fatalf("CleanupChildren: %v", err)
	}
	if cleaned.ReleasedTasks != 0 {
		t.Fatalf("dry run released %d", cleaned.ReleasedTasks)
	}
	held, err := h.store.Task(task)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if held.State != store.TaskInProgress {
		t.Fatalf("dry run released the claim: %+v", held)
	}
}
