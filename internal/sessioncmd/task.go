// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

type Task struct {
	ID           string   `json:"id" jsonschema:"task id to claim, finish or release"`
	Title        string   `json:"title" jsonschema:"one-line summary of the work"`
	Body         string   `json:"body,omitempty" jsonschema:"full instruction for whoever claims it"`
	State        string   `json:"state" jsonschema:"pending, in_progress or done"`
	Owner        string   `json:"owner,omitempty" jsonschema:"session id holding the claim"`
	OwnerName    string   `json:"owner_name,omitempty" jsonschema:"name of the session holding the claim"`
	CreatedBy    string   `json:"created_by,omitempty" jsonschema:"session id that created the task; empty on tasks from before creators were recorded"`
	CreatorName  string   `json:"created_by_name,omitempty" jsonschema:"name of the session that created the task; empty once its row is gone, when created_by alone names it"`
	Assignee     string   `json:"assignee,omitempty" jsonschema:"session id the task is assigned to; only it may claim the task while it is pending"`
	AssigneeName string   `json:"assignee_name,omitempty" jsonschema:"name of the session the task is assigned to"`
	DependsOn    []string `json:"depends_on,omitempty" jsonschema:"task ids that must be done before this one can be claimed"`
	BlockedBy    []string `json:"blocked_by,omitempty" jsonschema:"the dependencies that are not done yet; the rest of depends_on are finished and in nobody's way"`
	Blocked      bool     `json:"blocked" jsonschema:"whether unfinished dependencies currently stop this task being claimed"`
	Mine         bool     `json:"mine" jsonschema:"whether the calling session holds the claim"`
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
	// CreatedBy keeps only tasks this session created: "me" for the
	// caller, or a session id.
	CreatedBy string
	// Fleet keeps only tasks the caller's fleet created; see fleetOf.
	Fleet bool
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

func (o TaskListOptions) query(callerID string, fleet func() ([]string, error)) (store.TaskQuery, error) {
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
	creator := strings.TrimSpace(o.CreatedBy)
	if strings.EqualFold(creator, "me") {
		creator = callerID
	}
	if o.Fleet {
		members, err := fleet()
		if err != nil {
			return store.TaskQuery{}, err
		}
		query.CreatedBy = members
		// Both filters keep the intersection: a creator outside the fleet
		// matches nothing rather than widening the read.
		if creator != "" {
			query.CreatedBy = []string{}
			if slices.Contains(members, creator) {
				query.CreatedBy = []string{creator}
			}
		}
	} else if creator != "" {
		query.CreatedBy = []string{creator}
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
	query, err := opts.query(caller.ID, func() ([]string, error) { return runtime.fleetOf(caller) })
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
			ID:           task.ID,
			Title:        task.Title,
			Body:         task.Body,
			State:        task.State,
			Owner:        task.Owner,
			OwnerName:    names[task.Owner],
			CreatedBy:    task.CreatedBy,
			CreatorName:  names[task.CreatedBy],
			Assignee:     task.Assignee,
			AssigneeName: names[task.Assignee],
			DependsOn:    task.DependsOn,
			BlockedBy:    task.Blocking,
			Blocked:      task.State == store.TaskPending && len(task.Blocking) > 0,
			Mine:         task.Owner == sessionID,
		})
	}
	return tasks, nil
}

// fleetOf is the sessions whose tasks count as the caller's own work: the
// caller, the session that spawned it and the parent it is drawn under
// (one and the same unless it was re-filed), and every session it spawned
// or has drawn under it, archived ones included until their rows are
// deleted. Siblings are left out on purpose: a worker takes from the plan
// its lead put up, not from what another worker split off for itself, and
// the lead, whose children they all are, still sees both.
func (r *runtime) fleetOf(caller store.Session) ([]string, error) {
	members := []string{caller.ID}
	add := func(id string) {
		if id != "" && !slices.Contains(members, id) {
			members = append(members, id)
		}
	}
	add(caller.SpawnedBy)
	add(caller.ParentID)
	sessions, err := r.store.ListSessions(true)
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		if sess.ID != caller.ID && (store.SpawnerOf(sess) == caller.ID || sess.ParentID == caller.ID) {
			add(sess.ID)
		}
	}
	return members, nil
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

// TaskSpec is a task to put on the list.
type TaskSpec struct {
	Title     string
	Body      string
	DependsOn []string
	// Assignee is the only session that may claim it: "me" for the caller,
	// or a session id. Empty leaves it to anyone.
	Assignee string
}

// newTask shapes spec into a row creator puts on the list, refusing what
// could never be a task.
func newTask(spec TaskSpec, creator string, now time.Time) (store.Task, error) {
	title := strings.TrimSpace(spec.Title)
	if title == "" {
		return store.Task{}, errors.New("title is empty")
	}
	return store.Task{
		ID:        uuid.NewString()[:8],
		Title:     title,
		Body:      strings.TrimSpace(spec.Body),
		State:     store.TaskPending,
		CreatedBy: creator,
		DependsOn: spec.DependsOn,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (r *runtime) missingDependency(err error) error {
	var missing *store.MissingDependencyError
	if errors.As(err, &missing) {
		return fmt.Errorf("dependency %s does not exist; create it first, or call %s for current ids", missing.ID, r.words.ListTasks)
	}
	return err
}

func (s *Sessions) CreateTask(sessionID string, spec TaskSpec) (Task, error) {
	if strings.TrimSpace(spec.Title) == "" {
		return Task{}, errors.New("title is empty")
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
	task, err := newTask(spec, caller.ID, time.Now())
	if err != nil {
		return Task{}, err
	}
	if task.Assignee, err = runtime.assignee(caller, spec.Assignee); err != nil {
		return Task{}, err
	}
	if err := runtime.store.CreateTask(task); err != nil {
		return Task{}, runtime.missingDependency(err)
	}
	return runtime.task(sessionID, task.ID)
}

// assignee resolves who a task is being assigned to: "me" is the caller,
// and anything else must be an agent session that is still on the board,
// since a task assigned to an archived row or a terminal would wait on a
// claim nobody is going to make. Empty is nobody.
func (r *runtime) assignee(caller store.Session, raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", nil
	}
	if strings.EqualFold(id, "me") {
		return caller.ID, nil
	}
	sess, err := r.store.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("assignee %s is not a session; call %s for current ids", id, r.words.ListSessions)
	}
	if err != nil {
		return "", err
	}
	if sess.Archived {
		return "", fmt.Errorf("assignee %s is archived, so it would never claim the task", id)
	}
	if r.cfg.Tools[sess.Tool].Shell {
		return "", fmt.Errorf("assignee %s is a terminal, not an agent", id)
	}
	return sess.ID, nil
}

// AssignTask points a pending task at one session, the only one that may
// then claim it, or at nobody when assignee is empty. Its creator or its
// current assignee may move it; a task with neither is anyone's to assign.
func (s *Sessions) AssignTask(sessionID, taskID, assignee string) (Task, error) {
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
	target, err := runtime.assignee(caller, assignee)
	if err != nil {
		return Task{}, err
	}
	changed, err := runtime.store.AssignTask(taskID, target, caller.ID, time.Now())
	if err != nil {
		return Task{}, err
	}
	if !changed {
		return Task{}, runtime.refusedAssignment(caller.ID, taskID)
	}
	return runtime.task(caller.ID, taskID)
}

// refusedAssignment reads the task back to say why it could not be moved.
func (r *runtime) refusedAssignment(sessionID, taskID string) error {
	task, err := r.task(sessionID, taskID)
	if err != nil {
		return err
	}
	if task.State != store.TaskPending {
		return fmt.Errorf("task %s is %s; only a pending task can be assigned", taskID, task.State)
	}
	if task.Assignee == "" && task.CreatedBy == "" {
		return fmt.Errorf("task %s could not be assigned", taskID)
	}
	who := []string{}
	if task.CreatedBy != "" {
		who = append(who, "its creator "+nameOr(task.CreatorName, task.CreatedBy))
	}
	if task.Assignee != "" {
		who = append(who, "its assignee "+nameOr(task.AssigneeName, task.Assignee))
	}
	return fmt.Errorf("task %s is not yours to assign; only %s can move it", taskID, strings.Join(who, " or "))
}

func nameOr(name, id string) string {
	if name != "" {
		return name
	}
	return id
}

// ClaimOptions changes what a claim with no task id may take.
type ClaimOptions struct {
	// Any takes the oldest ready task on the whole list instead of only
	// the caller's fleet's.
	Any bool
}

// ClaimTask takes a named task, or with no id the oldest unblocked one
// assigned to the caller, then the oldest unassigned one its fleet
// created. A task another session already holds
// is refused rather than stolen: the loser of the race finds out instead
// of duplicating work. The fleet scope is what keeps a worker from picking
// up a stale task some unrelated workstream left on the list months ago.
func (s *Sessions) ClaimTask(sessionID, taskID string, opts ClaimOptions) (Task, error) {
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
		var creators []string
		if !opts.Any {
			if creators, err = runtime.fleetOf(caller); err != nil {
				return Task{}, err
			}
		}
		claimed, ok, err := runtime.store.ClaimNextTask(caller.ID, creators, now)
		if err != nil {
			return Task{}, err
		}
		if !ok {
			return Task{}, runtime.nothingReady(caller.ID, opts.Any)
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

// nothingReady says why a bare claim came back empty. A fleet-scoped one
// says how much ready work sits outside the fleet, since "nothing to do"
// and "nothing of yours to do" call for different next moves.
func (r *runtime) nothingReady(callerID string, anyCreator bool) error {
	empty := errors.New("no task is ready to claim; every pending task is blocked, or the list is empty")
	if anyCreator {
		return empty
	}
	outside, err := r.store.ReadyTaskCount(callerID)
	if err != nil {
		return err
	}
	if outside == 0 {
		return empty
	}
	return fmt.Errorf("no task your fleet created is ready to claim; %d ready %s outside it, created by unrelated sessions or before creators were recorded; call %s to take the oldest of those, or claim one by id",
		outside, plural(outside, "task is", "tasks are"), r.words.ClaimAny)
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func holderOf(task Task) string {
	return nameOr(task.OwnerName, task.Owner)
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
	case task.Assignee != "" && task.Assignee != sessionID:
		return fmt.Errorf("task %s is assigned to %s; only that session can claim it, and its creator or assignee can reassign it", taskID, nameOr(task.AssigneeName, task.Assignee))
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

// TaskDeletion is a delete and the work it set free.
type TaskDeletion struct {
	Task      Task   `json:"task" jsonschema:"the task as it stood when it was deleted"`
	Unblocked []Task `json:"unblocked,omitempty" jsonschema:"dependents the delete made claimable; only a forced delete of unfinished work has any, since its edges go with it"`
}

// DeleteTask removes a task, refusing one another session holds or one
// unfinished work waits on unless force is set. A forced delete takes the
// task's edges with it, so its dependents are unblocked as though it had
// been finished; the reply names them. Keeping them blocked instead would
// leave them on an edge that names no task, which nothing can ever clear.
func (s *Sessions) DeleteTask(sessionID, taskID string, force bool) (TaskDeletion, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return TaskDeletion{}, fmt.Errorf("task_id is empty; call %s to get one", s.words.ListTasks)
	}
	runtime, err := s.open()
	if err != nil {
		return TaskDeletion{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return TaskDeletion{}, err
	}
	deleted, found, err := runtime.store.DeleteTask(taskID, store.DeleteGuard{CallerID: caller.ID, Force: force})
	var refused *store.DeleteRefusedError
	if errors.As(err, &refused) {
		return TaskDeletion{}, runtime.refusedDelete(caller.ID, refused)
	}
	if err != nil {
		return TaskDeletion{}, err
	}
	if !found {
		return TaskDeletion{}, fmt.Errorf("task %s does not exist; call %s for current ids", taskID, runtime.words.ListTasks)
	}
	rows, err := runtime.taskRows(caller.ID, append([]store.Task{deleted.Task}, deleted.Unblocked...))
	if err != nil {
		return TaskDeletion{}, err
	}
	return TaskDeletion{Task: rows[0], Unblocked: rows[1:]}, nil
}

// maxNamedTasks bounds how many tasks one sentence names; the rest are
// counted.
const maxNamedTasks = 10

// refusedDelete says why a delete did not go and how to make it go anyway,
// naming the holder or assignee and the dependents so the caller can ask
// the one or clear the others first.
func (r *runtime) refusedDelete(callerID string, refused *store.DeleteRefusedError) error {
	var reasons []string
	names, _ := r.sessionNames()
	named := func(id string) string {
		if names[id] != "" {
			return names[id]
		}
		return id
	}
	if refused.HeldBy != "" {
		reasons = append(reasons, fmt.Sprintf("it is in progress, held by %s, and deleting it pulls the work out from under that session; ask it to finish or release the task", named(refused.HeldBy)))
	}
	if refused.AssignedTo != "" {
		reasons = append(reasons, fmt.Sprintf("it is assigned to %s, which is waiting to claim it; ask that session or the task's creator before dropping work handed to it", named(refused.AssignedTo)))
	}
	if len(refused.Dependents) > 0 {
		dependents, err := r.taskRows(callerID, refused.Dependents)
		if err != nil {
			return err
		}
		reasons = append(reasons, fmt.Sprintf("%d unfinished %s on it: %s; deleting it makes them claimable although it was never done, so delete or finish them first",
			len(dependents), plural(len(dependents), "task waits", "tasks wait"), namedTasks(dependents)))
	}
	return fmt.Errorf("task %s was not deleted: %s. Call %s to delete it anyway; its dependents are then unblocked",
		refused.Task.ID, strings.Join(reasons, "; and "), r.words.ForceDelete)
}

// namedTasks lists tasks as "title (id) [state]", up to maxNamedTasks.
func namedTasks(tasks []Task) string {
	names := make([]string, 0, min(len(tasks), maxNamedTasks)+1)
	for i, task := range tasks {
		if i == maxNamedTasks {
			names = append(names, fmt.Sprintf("and %d more", len(tasks)-i))
			break
		}
		names = append(names, fmt.Sprintf("%s (%s) [%s]", task.Title, task.ID, task.State))
	}
	return strings.Join(names, ", ")
}

const (
	// DefaultPruneDays is how old a stale task must be before prune takes
	// it. Two weeks is past any workstream still coming back to its plan.
	DefaultPruneDays = 14
	// MaxPruneDays bounds the age a caller can ask for, so a typo is a
	// refusal rather than a prune that silently matches nothing.
	MaxPruneDays = 3650
)

// TaskPruneOptions narrows a prune. The zero value is the dry run over
// tasks older than DefaultPruneDays.
type TaskPruneOptions struct {
	// OlderThanDays is the age past which a stale task is swept; zero
	// takes DefaultPruneDays.
	OlderThanDays int
	// Delete deletes what the prune finds. Without it the prune only
	// reports, which is the default because a delete cannot be undone.
	Delete bool
}

// TaskPrune is what a prune found and, unless it was a dry run, deleted.
type TaskPrune struct {
	DryRun        bool   `json:"dry_run" jsonschema:"true when nothing was deleted and tasks is what a prune with dry_run false would delete"`
	OlderThanDays int    `json:"older_than_days" jsonschema:"the age in days past which a stale task was swept"`
	Count         int    `json:"count" jsonschema:"how many tasks are in tasks"`
	Tasks         []Task `json:"tasks" jsonschema:"the stale tasks deleted, or on a dry run those that would be: pending, older than older_than_days, and created by a session that is archived, deleted or was never recorded"`
	Kept          []Task `json:"kept,omitempty" jsonschema:"stale tasks left alone because a task that depends on them is in progress"`
	Unblocked     []Task `json:"unblocked,omitempty" jsonschema:"surviving tasks the prune made claimable by deleting what they waited on"`
}

// PruneTasks sweeps the pending tasks nobody is left to want: older than
// the cutoff, and put up by a session that is archived, deleted, or from
// before creators were recorded. Those are what pile up on a long-lived
// board, since nothing else ever takes a pending task off it.
func (s *Sessions) PruneTasks(sessionID string, opts TaskPruneOptions) (TaskPrune, error) {
	days := opts.OlderThanDays
	switch {
	case days == 0:
		days = DefaultPruneDays
	case days < 0 || days > MaxPruneDays:
		return TaskPrune{}, fmt.Errorf("older_than_days %d is out of range; ask for between 1 and %d", opts.OlderThanDays, MaxPruneDays)
	}
	runtime, err := s.open()
	if err != nil {
		return TaskPrune{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return TaskPrune{}, err
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	pruned, err := runtime.store.PruneTasks(cutoff, !opts.Delete)
	if err != nil {
		return TaskPrune{}, err
	}
	result := TaskPrune{DryRun: !opts.Delete, OlderThanDays: days, Count: len(pruned.Tasks)}
	for _, rows := range []struct {
		from []store.Task
		to   *[]Task
	}{{pruned.Tasks, &result.Tasks}, {pruned.Kept, &result.Kept}, {pruned.Unblocked, &result.Unblocked}} {
		if *rows.to, err = runtime.taskRows(caller.ID, rows.from); err != nil {
			return TaskPrune{}, err
		}
	}
	return result, nil
}

// unassignEnded hands the tasks assigned to a session somebody just killed
// back to anyone. Only the deliberate kills call it, not the kill a restart
// or an account switch makes on the way to relaunching the same session,
// which still wants its work; archives clear it in the store. The kill has
// already happened, so a failure here is logged rather than reported as
// the kill failing.
func (r *runtime) unassignEnded(id string) {
	if err := r.store.UnassignTasksOf(id, time.Now()); err != nil {
		logging.Warn("could not unassign a killed session's tasks", "session", id, logging.Err(err))
	}
}
