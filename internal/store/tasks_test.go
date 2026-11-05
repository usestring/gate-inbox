// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
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

// danglingEdge writes an edge naming no task, which CreateTask now refuses
// but a list written before it checked inside its transaction can hold.
func danglingEdge(t *testing.T, st *Store, id, dep string) {
	t.Helper()
	if _, err := st.db.Exec(`INSERT INTO task_deps (task_id, depends_on_id) VALUES (?, ?)`, id, dep); err != nil {
		t.Fatalf("insert edge %s -> %s: %v", id, dep, err)
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

	_, deleted, err := st.DeleteTask("feature", DeleteGuard{Force: true})
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
	createTask(t, st, "feature")
	danglingEdge(t, st, "feature", "groundwork")

	_, deleted, err := st.DeleteTask("groundwork", DeleteGuard{})
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
	next, taken, err := st.ClaimNextTask("session01", nil, time.Now())
	if err != nil {
		t.Fatalf("ClaimNextTask: %v", err)
	}
	if taken {
		t.Fatalf("a task the list reports blocked was handed out as the next one: %+v", next)
	}
}

// A dependency deleted between the check and the edge would leave an edge
// naming no task, blocking the new one for good. The check runs inside the
// insert's transaction, so a missing dependency refuses the whole create
// and writes neither the row nor any edge.
func TestCreateTaskRefusesAMissingDependencyAtomically(t *testing.T) {
	st := newTestStore(t)
	createTask(t, st, "groundwork")
	now := time.Now()
	err := st.CreateTask(Task{
		ID: "feature", Title: "work on feature", State: TaskPending,
		DependsOn: []string{"groundwork", "gone"}, CreatedAt: now, UpdatedAt: now,
	})
	var missing *MissingDependencyError
	if !errors.As(err, &missing) || missing.ID != "gone" {
		t.Fatalf("CreateTask with a missing dependency = %v", err)
	}
	if _, err := st.Task("feature"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("the refused task was written anyway: %v", err)
	}
	if edges := edgeCount(t, st); edges != 0 {
		t.Fatalf("the refused create left %d edges", edges)
	}
	// A task cannot wait on itself: its own row is in the transaction, but
	// it is not a dependency anybody created first.
	if err := st.CreateTask(Task{ID: "loop", Title: "loop", State: TaskPending, DependsOn: []string{"loop"}, CreatedAt: now, UpdatedAt: now}); !errors.As(err, &missing) {
		t.Fatalf("a task depending on itself = %v", err)
	}
}

// A dependency deleted while another session creates a task depending on
// it must leave either the delete or the create refused, never a new task
// blocked by an edge naming nothing.
func TestRacingDeleteAndCreateNeverLeaveADanglingEdge(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "state.db")
	for round := range 20 {
		creator, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		deleter, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		dep := fmt.Sprintf("dep-%d", round)
		createTask(t, creator, dep)
		start := make(chan struct{})
		var running sync.WaitGroup
		running.Add(2)
		go func() {
			defer running.Done()
			<-start
			now := time.Now()
			_ = creator.CreateTask(Task{ID: fmt.Sprintf("feat-%d", round), Title: "feature", State: TaskPending, DependsOn: []string{dep}, CreatedAt: now, UpdatedAt: now})
		}()
		go func() {
			defer running.Done()
			<-start
			_, _, _ = deleter.DeleteTask(dep, DeleteGuard{Force: true})
		}()
		close(start)
		running.Wait()
		var dangling int
		if err := creator.db.QueryRow(`SELECT count(*) FROM task_deps d LEFT JOIN tasks p ON p.id = d.depends_on_id WHERE p.id IS NULL`).Scan(&dangling); err != nil {
			t.Fatalf("count dangling edges: %v", err)
		}
		if dangling != 0 {
			t.Fatalf("round %d left %d edges naming no task", round, dangling)
		}
		creator.Close()
		deleter.Close()
	}
}

// Creators narrow a bare claim to the work a fleet put up, and nil is the
// whole list again.
func TestClaimNextTaskTakesOnlyFromTheNamedCreators(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	for i, task := range []Task{
		{ID: "stale", CreatedBy: ""},
		{ID: "theirs", CreatedBy: "other001"},
		{ID: "ours", CreatedBy: "lead0001"},
	} {
		task.Title, task.State = "work on "+task.ID, TaskPending
		task.CreatedAt = now.Add(time.Duration(i) * time.Second)
		task.UpdatedAt = task.CreatedAt
		if err := st.CreateTask(task); err != nil {
			t.Fatalf("CreateTask(%s): %v", task.ID, err)
		}
	}
	next, taken, err := st.ClaimNextTask("worker01", []string{"worker01", "lead0001"}, now)
	if err != nil || !taken || next.ID != "ours" || next.CreatedBy != "lead0001" {
		t.Fatalf("fleet claim = %+v, %t, %v", next, taken, err)
	}
	if _, taken, err := st.ClaimNextTask("worker01", []string{"worker01", "lead0001"}, now); err != nil || taken {
		t.Fatalf("a drained fleet handed out another task: %t, %v", taken, err)
	}
	ready, err := st.ReadyTaskCount()
	if err != nil || ready != 2 {
		t.Fatalf("ReadyTaskCount = %d, %v; want the two outside the fleet", ready, err)
	}
	next, taken, err = st.ClaimNextTask("worker01", nil, now)
	if err != nil || !taken || next.ID != "stale" {
		t.Fatalf("global claim = %+v, %t, %v; want the oldest task", next, taken, err)
	}
	page, err := st.QueryTasks(TaskQuery{CreatedBy: []string{"other001"}})
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != "theirs" || page.Matched != 1 {
		t.Fatalf("created_by query = %+v, %v", page, err)
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
