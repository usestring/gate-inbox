// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/store"
)

type Task struct {
	ID        string   `json:"id" jsonschema:"task id to claim, finish or release"`
	Title     string   `json:"title" jsonschema:"one-line summary of the work"`
	Body      string   `json:"body,omitempty" jsonschema:"full instruction for whoever claims it"`
	State     string   `json:"state" jsonschema:"pending, in_progress or done"`
	Owner     string   `json:"owner,omitempty" jsonschema:"session id holding the claim"`
	OwnerName string   `json:"owner_name,omitempty" jsonschema:"name of the session holding the claim"`
	DependsOn []string `json:"depends_on,omitempty" jsonschema:"task ids that must be done before this one can be claimed"`
	BlockedBy []string `json:"blocked_by,omitempty" jsonschema:"the dependencies that are not done yet; the rest of depends_on are finished and in nobody's way"`
	Blocked   bool     `json:"blocked" jsonschema:"whether unfinished dependencies currently stop this task being claimed"`
	Mine      bool     `json:"mine" jsonschema:"whether the calling session holds the claim"`
}

// A list that has run for a while is mostly finished work, and every row
// carried its full instruction, so an agent checking what is left to claim
// paid for every task anyone had ever done. These bound what a list returns
// unless the caller asks for more.
const (
	DefaultTaskLimit = 50
	// MaxTaskLimit is well past a list anybody reads row by row; a caller
	// that wants the whole history still has to say so.
	MaxTaskLimit = 500
)

// TaskListOptions narrows the task list. The zero value is the cheap read:
// work that is still open, without bodies, the first DefaultTaskLimit rows.
type TaskListOptions struct {
	// State keeps only tasks in these states. Empty keeps pending and
	// in_progress, which is the work somebody can still act on.
	State []string
	// IncludeDone adds finished tasks to whatever State keeps.
	IncludeDone bool
	// Mine keeps only tasks the caller holds or finished.
	Mine bool
	// IncludeBody carries each task's full instruction. A claim returns the
	// body anyway, so a caller choosing what to claim reads titles.
	IncludeBody bool
	// Limit caps the rows returned, after filtering. Zero takes
	// DefaultTaskLimit and anything over MaxTaskLimit is refused.
	Limit int
}

// TaskList carries the rows plus what the filters and the limit hid, for
// the same reason SessionList does: an agent that silently saw 50 of 80
// open tasks would plan against a wrong list.
type TaskList struct {
	Tasks    []Task `json:"tasks"`
	Matched  int    `json:"matched" jsonschema:"how many tasks matched the filters, before limit"`
	Returned int    `json:"returned" jsonschema:"how many rows are in tasks"`
	// Truncated is stated rather than left to matched > returned, so a
	// caller reading the structured payload does not have to derive it.
	Truncated bool `json:"truncated" jsonschema:"true when limit left matching tasks out; narrow state or mine, or raise limit"`
}

func (o TaskListOptions) query(callerID string) (store.TaskQuery, error) {
	query := store.TaskQuery{SkipBody: !o.IncludeBody}
	seen := map[string]bool{}
	for _, raw := range o.State {
		state := strings.ToLower(strings.TrimSpace(raw))
		switch state {
		case store.TaskPending, store.TaskInProgress, store.TaskDone:
		default:
			return store.TaskQuery{}, fmt.Errorf("unknown task state %q; use pending, in_progress or done", raw)
		}
		if !seen[state] {
			seen[state] = true
			query.States = append(query.States, state)
		}
	}
	if len(query.States) == 0 {
		query.States = []string{store.TaskPending, store.TaskInProgress}
	}
	if o.IncludeDone && !seen[store.TaskDone] {
		query.States = append(query.States, store.TaskDone)
	}
	if o.Mine {
		query.Owner = callerID
	}
	switch {
	case o.Limit == 0:
		query.Limit = DefaultTaskLimit
	case o.Limit < 0 || o.Limit > MaxTaskLimit:
		return store.TaskQuery{}, fmt.Errorf("limit %d is out of range; ask for between 1 and %d", o.Limit, MaxTaskLimit)
	default:
		query.Limit = o.Limit
	}
	return query, nil
}

// Tasks is the shared work list every session in the manager can see and
// claim from, so a fleet coordinates without one agent brokering handoffs.
func (s *Sessions) Tasks(sessionID string, opts TaskListOptions) (TaskList, error) {
	runtime, err := s.open()
	if err != nil {
		return TaskList{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return TaskList{}, err
	}
	query, err := opts.query(caller.ID)
	if err != nil {
		return TaskList{}, err
	}
	page, err := runtime.store.QueryTasks(query)
	if err != nil {
		return TaskList{}, err
	}
	tasks, err := runtime.taskRows(caller.ID, page.Tasks)
	if err != nil {
		return TaskList{}, err
	}
	return TaskList{
		Tasks:     tasks,
		Matched:   page.Matched,
		Returned:  len(tasks),
		Truncated: page.Matched > len(tasks),
	}, nil
}

func (r *runtime) taskRows(sessionID string, stored []store.Task) ([]Task, error) {
	names, err := r.sessionNames()
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(stored))
	for _, task := range stored {
		tasks = append(tasks, Task{
			ID:        task.ID,
			Title:     task.Title,
			Body:      task.Body,
			State:     task.State,
			Owner:     task.Owner,
			OwnerName: names[task.Owner],
			DependsOn: task.DependsOn,
			BlockedBy: task.Blocking,
			Blocked:   task.State == store.TaskPending && len(task.Blocking) > 0,
			Mine:      task.Owner == sessionID,
		})
	}
	return tasks, nil
}

func (r *runtime) sessionNames() (map[string]string, error) {
	sessions, err := r.store.ListSessions(true)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(sessions))
	for _, sess := range sessions {
		names[sess.ID] = sess.Name
	}
	return names, nil
}

func (r *runtime) task(sessionID, id string) (Task, error) {
	stored, err := r.store.Task(id)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("task %s does not exist; call %s for current ids", id, r.words.ListTasks)
	}
	if err != nil {
		return Task{}, err
	}
	tasks, err := r.taskRows(sessionID, []store.Task{stored})
	if err != nil {
		return Task{}, err
	}
	return tasks[0], nil
}

func (s *Sessions) CreateTask(sessionID, title, body string, dependsOn []string) (Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, errors.New("title is empty")
	}
	runtime, err := s.open()
	if err != nil {
		return Task{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Task{}, err
	}
	for _, dep := range dependsOn {
		if _, err := runtime.store.Task(dep); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Task{}, fmt.Errorf("dependency %s does not exist; create it first, or call %s for current ids", dep, runtime.words.ListTasks)
			}
			return Task{}, err
		}
	}
	now := time.Now()
	task := store.Task{
		ID:        uuid.NewString()[:8],
		Title:     title,
		Body:      strings.TrimSpace(body),
		State:     store.TaskPending,
		DependsOn: dependsOn,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := runtime.store.CreateTask(task); err != nil {
		return Task{}, err
	}
	return runtime.task(sessionID, task.ID)
}

// ClaimTask takes a named task, or the oldest unblocked one when no id is
// given. A task another session already holds is refused rather than
// stolen: the loser of the race finds out instead of duplicating work.
func (s *Sessions) ClaimTask(sessionID, taskID string) (Task, error) {
	runtime, err := s.open()
	if err != nil {
		return Task{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return Task{}, err
	}
	now := time.Now()
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		claimed, ok, err := runtime.store.ClaimNextTask(caller.ID, now)
		if err != nil {
			return Task{}, err
		}
		if !ok {
			return Task{}, errors.New("no task is ready to claim; every pending task is blocked, or the list is empty")
		}
		return runtime.task(caller.ID, claimed.ID)
	}
	claimed, err := runtime.store.ClaimTask(taskID, caller.ID, now)
	if err != nil {
		return Task{}, err
	}
	if !claimed {
		return Task{}, runtime.refusedClaim(caller.ID, taskID)
	}
	return runtime.task(caller.ID, taskID)
}

func holderOf(task Task) string {
	if task.OwnerName != "" {
		return task.OwnerName
	}
	return task.Owner
}

// refusedClaim reads the task back to say why the claim did not take,
// since "someone else got there first" and "its dependencies are not
// done" call for different next moves.
func (r *runtime) refusedClaim(sessionID, taskID string) error {
	task, err := r.task(sessionID, taskID)
	if err != nil {
		return err
	}
	switch {
	case task.State == store.TaskInProgress:
		return fmt.Errorf("task %s is already claimed by %s", taskID, holderOf(task))
	case task.State == store.TaskDone:
		return fmt.Errorf("task %s is already done", taskID)
	case task.Blocked:
		return fmt.Errorf("task %s is blocked on %s", taskID, strings.Join(task.BlockedBy, ", "))
	}
	return fmt.Errorf("task %s could not be claimed", taskID)
}

func (s *Sessions) FinishTask(sessionID, taskID string) (Task, error) {
	return s.settleTask(sessionID, taskID, true)
}

func (s *Sessions) ReleaseTask(sessionID, taskID string) (Task, error) {
	return s.settleTask(sessionID, taskID, false)
}

func (s *Sessions) settleTask(sessionID, taskID string, done bool) (Task, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return Task{}, fmt.Errorf("task_id is empty; call %s to get one", s.words.ListTasks)
	}
	runtime, err := s.open()
	if err != nil {
		return Task{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return Task{}, err
	}
	now := time.Now()
	settle := runtime.store.ReleaseTask
	if done {
		settle = runtime.store.FinishTask
	}
	changed, err := settle(taskID, caller.ID, now)
	if err != nil {
		return Task{}, err
	}
	if !changed {
		task, err := runtime.task(caller.ID, taskID)
		if err != nil {
			return Task{}, err
		}
		if task.State == store.TaskInProgress && task.Owner != caller.ID {
			return Task{}, fmt.Errorf("task %s is not yours to settle; %s holds it", taskID, holderOf(task))
		}
		return Task{}, fmt.Errorf("task %s is %s, not in progress", taskID, task.State)
	}
	return runtime.task(caller.ID, taskID)
}

func (s *Sessions) DeleteTask(sessionID, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task_id is empty; call %s to get one", s.words.ListTasks)
	}
	runtime, err := s.open()
	if err != nil {
		return err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return err
	}
	deleted, err := runtime.store.DeleteTask(taskID)
	if err != nil {
		return err
	}
	if !deleted {
		return fmt.Errorf("task %s does not exist; call %s for current ids", taskID, runtime.words.ListTasks)
	}
	return nil
}
