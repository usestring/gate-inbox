// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package store

import (
	"testing"
	"time"
)

func createTask(t *testing.T, st *Store, id string, dependsOn ...string) {
	t.Helper()
	now := time.Now()
	if err := st.CreateTask(Task{
		ID: id, Title: "work on " + id, State: TaskPending,
		DependsOn: dependsOn, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask(%s): %v", id, err)
	}
}

func edgeCount(t *testing.T, st *Store) int {
	t.Helper()
	var edges int
	if err := st.db.QueryRow(`SELECT count(*) FROM task_deps`).Scan(&edges); err != nil {
		t.Fatalf("count task_deps: %v", err)
	}
	return edges
}

func TestDeleteTaskTakesTheEdgesNamingItAlong(t *testing.T) {
	st := newTestStore(t)
	createTask(t, st, "groundwork")
	createTask(t, st, "feature", "groundwork")
	createTask(t, st, "release", "feature")

	deleted, err := st.DeleteTask("feature")
	if err != nil || !deleted {
		t.Fatalf("DeleteTask = %t, %v", deleted, err)
	}
	if edges := edgeCount(t, st); edges != 0 {
		t.Fatalf("both edges named the deleted task, %d survived", edges)
	}
	tasks, err := st.Tasks()
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks after the delete = %+v", tasks)
	}
	for _, task := range tasks {
		if len(task.DependsOn) != 0 {
			t.Fatalf("%s still depends on %v", task.ID, task.DependsOn)
		}
	}
}

// The id is whatever an agent typed, and a task may name a dependency that
// was never created, so a delete that matches no task has to leave the list
// exactly as it found it rather than quietly unblocking the work.
func TestDeleteTaskLeavesTheEdgesOfAnIDThatNamesNoTask(t *testing.T) {
	st := newTestStore(t)
	createTask(t, st, "feature", "groundwork")

	deleted, err := st.DeleteTask("groundwork")
	if err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if deleted {
		t.Fatal("an id naming no task reported a deletion")
	}
	if edges := edgeCount(t, st); edges != 1 {
		t.Fatalf("edges after a delete that matched nothing = %d, want 1", edges)
	}
	blocked, err := st.Task("feature")
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if len(blocked.DependsOn) != 1 || blocked.DependsOn[0] != "groundwork" {
		t.Fatalf("the surviving task lost what it waits on: %+v", blocked)
	}
	claimed, err := st.ClaimTask("feature", "session01", time.Now())
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed {
		t.Fatal("a task the list reports blocked was handed out by id")
	}
	next, taken, err := st.ClaimNextTask("session01", time.Now())
	if err != nil {
		t.Fatalf("ClaimNextTask: %v", err)
	}
	if taken {
		t.Fatalf("a task the list reports blocked was handed out as the next one: %+v", next)
	}
}

// A list that has run for a while is mostly finished work. The narrowing
// has to happen in the statement, or every finished row and every body is
// still read to be thrown away; and the count has to be of what matched,
// or a caller cannot tell a short list from a cut one.
func TestQueryTasksNarrowsAndCountsInTheStatement(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	for _, id := range []string{"a-schema", "b-backfill", "c-docs", "d-verify"} {
		if err := st.CreateTask(Task{
			ID: id, Title: "work on " + id, Body: "the long instruction for " + id, State: TaskPending,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("CreateTask(%s): %v", id, err)
		}
	}
	createTask(t, st, "e-release", "a-schema", "b-backfill")
	for _, id := range []string{"a-schema", "b-backfill"} {
		if ok, err := st.ClaimTask(id, "session01", now); err != nil || !ok {
			t.Fatalf("ClaimTask(%s) = %t, %v", id, ok, err)
		}
	}
	if ok, err := st.FinishTask("a-schema", "session01", now); err != nil || !ok {
		t.Fatalf("FinishTask = %t, %v", ok, err)
	}

	open, err := st.QueryTasks(TaskQuery{States: []string{TaskPending, TaskInProgress}, SkipBody: true})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if open.Matched != 4 || len(open.Tasks) != 4 {
		t.Fatalf("open work = %d rows of %d matched, want 4 of 4: %+v", len(open.Tasks), open.Matched, open.Tasks)
	}
	for _, task := range open.Tasks {
		if task.State == TaskDone {
			t.Fatalf("a done task came back from a read of the open work: %+v", task)
		}
		if task.Body != "" {
			t.Fatalf("%s carried its body through a read that skipped bodies", task.ID)
		}
		// The finished dependency is not on this page, and that must not
		// make it read as unfinished.
		if task.ID == "e-release" && (len(task.Blocking) != 1 || task.Blocking[0] != "b-backfill") {
			t.Fatalf("e-release is blocked on %v, want only b-backfill", task.Blocking)
		}
	}

	cut, err := st.QueryTasks(TaskQuery{States: []string{TaskPending}, Limit: 1})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if len(cut.Tasks) != 1 || cut.Matched != 3 || cut.Tasks[0].ID != "c-docs" {
		t.Fatalf("limited read = %d rows of %d matched, first %+v; want c-docs, 1 of 3", len(cut.Tasks), cut.Matched, cut.Tasks)
	}
	if cut.Tasks[0].Body == "" {
		t.Fatal("a read that did not skip bodies came back without one")
	}

	held, err := st.QueryTasks(TaskQuery{Owner: "session01"})
	if err != nil {
		t.Fatalf("QueryTasks: %v", err)
	}
	if held.Matched != 2 || len(held.Tasks) != 2 {
		t.Fatalf("owner read = %+v, want the finished and the claimed task", held.Tasks)
	}
}
