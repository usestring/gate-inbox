// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A claimant needs a row to call as, not a pane: the task list never
// touches tmux, so a race can run many more agents than panes.
func (h *sessionHarness) addSessionRow(t *testing.T, name string) string {
	t.Helper()
	id := uuid.NewString()[:8]
	if err := h.store.CreateSession(store.Session{
		ID: id, Name: name, Tool: h.caller.Tool, Cwd: h.caller.Cwd, Status: status.Idle,
	}); err != nil {
		t.Fatalf("create session row: %v", err)
	}
	return id
}

func racers(t *testing.T, h *sessionHarness, count int) []string {
	t.Helper()
	ids := make([]string, count)
	for i := range ids {
		ids[i] = h.addSessionRow(t, fmt.Sprintf("racer-%d", i))
	}
	return ids
}

func TestTasksAreClaimedByExactlyOneSession(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	rival, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "rival"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := h.sessions.CreateTask(h.caller.ID, "fix the retry backoff", "see internal/retry", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.State != "pending" || created.Blocked {
		t.Fatalf("new task = %+v", created)
	}

	claimed, err := h.sessions.ClaimTask(h.caller.ID, created.ID)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if claimed.State != "in_progress" || !claimed.Mine {
		t.Fatalf("claimed task = %+v", claimed)
	}
	// The second agent must be told, not silently allowed to duplicate work.
	if _, err := h.sessions.ClaimTask(rival.ID, created.ID); err == nil ||
		!strings.Contains(err.Error(), "already claimed by calling-agent") {
		t.Fatalf("rival claim error = %v", err)
	}
	// Naming the holder is what makes the refusal actionable: the reader's
	// next move is to message that session.
	if _, err := h.sessions.FinishTask(rival.ID, created.ID); err == nil ||
		!strings.Contains(err.Error(), "not yours to settle; calling-agent holds it") {
		t.Fatalf("rival finish error = %v", err)
	}

	finished, err := h.sessions.FinishTask(h.caller.ID, created.ID)
	if err != nil {
		t.Fatalf("FinishTask: %v", err)
	}
	if finished.State != "done" {
		t.Fatalf("finished task = %+v", finished)
	}

	// A task nobody is holding has no holder to name, however it got there.
	pending, err := h.sessions.CreateTask(h.caller.ID, "backfill the column", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, settled := range []struct {
		id   string
		want string
	}{
		{created.ID, "is done, not in progress"},
		{pending.ID, "is pending, not in progress"},
	} {
		if _, err := h.sessions.FinishTask(rival.ID, settled.id); err == nil ||
			!strings.Contains(err.Error(), settled.want) {
			t.Fatalf("settling %s = %v, want %q", settled.id, err, settled.want)
		}
	}
}

func TestDependenciesGateAClaimUntilTheyAreDone(t *testing.T) {
	h := newSessionHarness(t)
	first, err := h.sessions.CreateTask(h.caller.ID, "add the column", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	second, err := h.sessions.CreateTask(h.caller.ID, "backfill the column", "", []string{first.ID})
	if err != nil {
		t.Fatalf("CreateTask dependent: %v", err)
	}
	if !second.Blocked {
		t.Fatalf("dependent task should start blocked: %+v", second)
	}
	if _, err := h.sessions.ClaimTask(h.caller.ID, second.ID); err == nil ||
		!strings.Contains(err.Error(), "blocked on") {
		t.Fatalf("blocked claim error = %v", err)
	}
	// Claiming without an id must skip the blocked task and take the ready one.
	next, err := h.sessions.ClaimTask(h.caller.ID, "")
	if err != nil {
		t.Fatalf("ClaimTask next: %v", err)
	}
	if next.ID != first.ID {
		t.Fatalf("claim-next took the blocked task: %+v", next)
	}
	if _, err := h.sessions.FinishTask(h.caller.ID, first.ID); err != nil {
		t.Fatalf("FinishTask: %v", err)
	}
	unblocked, err := h.sessions.ClaimTask(h.caller.ID, "")
	if err != nil {
		t.Fatalf("ClaimTask after the dependency finished: %v", err)
	}
	if unblocked.ID != second.ID {
		t.Fatalf("finishing a dependency did not unblock its dependent: %+v", unblocked)
	}
	if _, err := h.sessions.ClaimTask(h.caller.ID, ""); err == nil ||
		!strings.Contains(err.Error(), "no task is ready") {
		t.Fatalf("empty-list claim error = %v", err)
	}
	if _, err := h.sessions.CreateTask(h.caller.ID, "depends on nothing real", "", []string{"nope1234"}); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unknown dependency error = %v", err)
	}
}

// A dependency that is done is in nobody's way, so naming it alongside the
// one that still blocks sends the reader after work that is already
// finished and listed as done two lines above.
func TestOnlyUnfinishedDependenciesAreNamedAsBlocking(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	first, err := h.sessions.CreateTask(h.caller.ID, "add the column", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	second, err := h.sessions.CreateTask(h.caller.ID, "write the migration", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	dependent, err := h.sessions.CreateTask(h.caller.ID, "backfill the column", "", []string{first.ID, second.ID})
	if err != nil {
		t.Fatalf("CreateTask dependent: %v", err)
	}
	if _, err := h.sessions.ClaimTask(h.caller.ID, first.ID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if _, err := h.sessions.FinishTask(h.caller.ID, first.ID); err != nil {
		t.Fatalf("FinishTask: %v", err)
	}

	_, err = h.sessions.ClaimTask(h.caller.ID, dependent.ID)
	if err == nil {
		t.Fatal("a task with an unfinished dependency should not be claimable")
	}
	if !strings.Contains(err.Error(), second.ID) || strings.Contains(err.Error(), first.ID) {
		t.Fatalf("the refusal names a finished dependency: %v", err)
	}

	listed, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	tasks := listed.Tasks
	text := FormatTaskList(listed)
	if !strings.Contains(text, "blocked on "+second.ID) {
		t.Fatalf("the list does not name what blocks the task:\n%s", text)
	}
	if strings.Contains(text, "blocked on "+first.ID) {
		t.Fatalf("the list reports a finished dependency as blocking:\n%s", text)
	}
	for _, task := range tasks {
		if task.ID == dependent.ID && (len(task.BlockedBy) != 1 || task.BlockedBy[0] != second.ID) {
			t.Fatalf("blocked_by = %v, want only %s", task.BlockedBy, second.ID)
		}
	}
}

func TestReleasedAndDeletedTasksLeaveTheList(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.CreateTask(h.caller.ID, "spike the cache", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := h.sessions.ClaimTask(h.caller.ID, created.ID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	released, err := h.sessions.ReleaseTask(h.caller.ID, created.ID)
	if err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}
	if released.State != "pending" || released.Owner != "" {
		t.Fatalf("released task = %+v", released)
	}
	if err := h.sessions.DeleteTask(h.caller.ID, created.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	listed, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{IncludeDone: true})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	tasks := listed.Tasks
	if len(tasks) != 0 {
		t.Fatalf("deleted task is still listed: %+v", tasks)
	}
	if err := h.sessions.DeleteTask(h.caller.ID, created.ID); err == nil {
		t.Fatal("deleting a task twice should report it is gone")
	}
}

func TestDeletingASessionHandsItsClaimsBack(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := h.sessions.CreateTask(h.caller.ID, "migrate the table", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := h.sessions.ClaimTask(worker.ID, created.ID); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if err := h.store.Delete(worker.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	listed, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{IncludeDone: true})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	tasks := listed.Tasks
	if len(tasks) != 1 || tasks[0].State != "pending" || tasks[0].Owner != "" {
		t.Fatalf("a deleted session left its claim parked: %+v", tasks)
	}
}

// The sequential claims above never reach the guard the atomic claim
// exists for: several agents reaching for the same task in one instant.
func TestRacingClaimsOnOneTaskLeaveASingleWinner(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.CreateTask(h.caller.ID, "fix the retry backoff", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	ids := racers(t, h, 6)

	start := make(chan struct{})
	claimed := make(chan Task, len(ids))
	refused := make(chan error, len(ids))
	var running sync.WaitGroup
	for _, id := range ids {
		running.Add(1)
		go func(sessionID string) {
			defer running.Done()
			<-start
			task, err := h.sessions.ClaimTask(sessionID, created.ID)
			if err != nil {
				refused <- err
				return
			}
			claimed <- task
		}(id)
	}
	close(start)
	running.Wait()
	close(claimed)
	close(refused)

	if len(claimed) != 1 {
		t.Fatalf("%d of %d racers claimed the same task", len(claimed), len(ids))
	}
	winner := <-claimed
	for err := range refused {
		if !strings.Contains(err.Error(), "already claimed by") {
			t.Fatalf("a losing racer was told %q, which does not say who holds it", err)
		}
	}
	listed, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{IncludeDone: true})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	stored := listed.Tasks
	if len(stored) != 1 || stored[0].State != "in_progress" || stored[0].Owner != winner.Owner {
		t.Fatalf("stored task = %+v, winner = %+v", stored, winner)
	}
}

// Claiming without an id is how a worker agent finds its own next job, so
// a fleet doing it at once must still split the list, never share a piece.
func TestRacingSessionsSplitTheListWithoutSharingATask(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	const taskCount = 5
	for i := range taskCount {
		if _, err := h.sessions.CreateTask(h.caller.ID, fmt.Sprintf("piece %d", i), "", nil); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}
	ids := racers(t, h, 8)

	start := make(chan struct{})
	// Room for the bounded worst case, so a racer that keeps winning fails
	// on its own bound rather than blocking on a full channel.
	claimed := make(chan Task, (taskCount+1)*len(ids))
	failed := make(chan error, len(ids))
	var running sync.WaitGroup
	for _, id := range ids {
		running.Add(1)
		go func(sessionID string) {
			defer running.Done()
			<-start
			for claims := 0; ; claims++ {
				task, err := h.sessions.ClaimTask(sessionID, "")
				if err != nil {
					// An empty list is how a racer learns it is done; any
					// other refusal is the list failing under contention.
					if !strings.Contains(err.Error(), "no task is ready to claim") {
						failed <- err
					}
					return
				}
				claimed <- task
				// A list that keeps handing out more than it holds would
				// otherwise spin here instead of failing.
				if claims >= taskCount {
					failed <- fmt.Errorf("one racer claimed %d tasks from a list of %d", claims+1, taskCount)
					return
				}
			}
		}(id)
	}
	close(start)
	running.Wait()
	close(claimed)
	close(failed)

	for err := range failed {
		t.Fatalf("the list failed a racer: %v", err)
	}
	owners := map[string]string{}
	for task := range claimed {
		if previous, taken := owners[task.ID]; taken {
			t.Fatalf("task %s was handed to %s and to %s", task.ID, previous, task.Owner)
		}
		owners[task.ID] = task.Owner
	}
	if len(owners) != taskCount {
		t.Fatalf("%d of %d tasks were claimed: %v", len(owners), taskCount, owners)
	}
	listed, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{IncludeDone: true})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	stored := listed.Tasks
	for _, task := range stored {
		if task.State != "in_progress" || task.Owner != owners[task.ID] {
			t.Fatalf("stored task %+v disagrees with the claim %s reported", task, owners[task.ID])
		}
	}
}

// An agent reads the list to find work, and on a list that has run for a
// while nearly every row is finished. The default read is the open work as
// titles, and a list the limit cut says so rather than passing for whole.
func TestTheTaskListDefaultsToOpenWorkAndSaysWhenItWasCut(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker := h.addSessionRow(t, "worker")
	ids := make([]string, 0, 4)
	for _, title := range []string{"add the column", "backfill it", "write the docs", "verify"} {
		created, err := h.sessions.CreateTask(h.caller.ID, title, "the full instruction for "+title, nil)
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		ids = append(ids, created.ID)
	}
	for _, id := range ids[:2] {
		if _, err := h.sessions.ClaimTask(h.caller.ID, id); err != nil {
			t.Fatalf("ClaimTask: %v", err)
		}
		if _, err := h.sessions.FinishTask(h.caller.ID, id); err != nil {
			t.Fatalf("FinishTask: %v", err)
		}
	}
	if _, err := h.sessions.ClaimTask(worker, ids[2]); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	open, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if open.Matched != 2 || open.Returned != 2 || open.Truncated {
		t.Fatalf("default list = %d of %d, truncated %t; want the 2 open tasks", open.Returned, open.Matched, open.Truncated)
	}
	for _, task := range open.Tasks {
		if task.State == store.TaskDone {
			t.Fatalf("the default list carried finished work: %+v", task)
		}
		if task.Body != "" {
			t.Fatalf("the default list carried %s's body", task.ID)
		}
	}

	everything, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{IncludeDone: true, IncludeBody: true, Limit: 3})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if everything.Matched != 4 || everything.Returned != 3 || !everything.Truncated {
		t.Fatalf("limited list = %d of %d, truncated %t; want 3 of 4, truncated", everything.Returned, everything.Matched, everything.Truncated)
	}
	if everything.Tasks[0].Body == "" {
		t.Fatal("a list that asked for bodies came back without them")
	}
	if text := FormatTaskList(everything); !strings.Contains(text, "(3 of 4 matching tasks; narrow state or mine, or raise limit)") {
		t.Fatalf("the rendering does not say the list was cut:\n%s", text)
	}

	done, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{State: []string{"done"}, Mine: true})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if done.Matched != 2 || !done.Tasks[0].Mine || !done.Tasks[1].Mine {
		t.Fatalf("the caller's finished work = %+v", done.Tasks)
	}
	theirs, err := h.sessions.Tasks(worker, TaskListOptions{Mine: true})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if theirs.Matched != 1 || theirs.Tasks[0].ID != ids[2] {
		t.Fatalf("the worker's own claims = %+v", theirs.Tasks)
	}

	if _, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{State: []string{"finished"}}); err == nil ||
		!strings.Contains(err.Error(), "use pending, in_progress or done") {
		t.Fatalf("an unknown state = %v", err)
	}
	if _, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{Limit: MaxTaskLimit + 1}); err == nil ||
		!strings.Contains(err.Error(), "out of range") {
		t.Fatalf("a limit past the maximum = %v", err)
	}
}
