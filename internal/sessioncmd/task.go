// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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
	Note         string   `json:"note,omitempty" jsonschema:"what the finisher said came of the task: the outcome, a PR link, what is left; set once it is done, and shortened in text replies unless include_body is set"`
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
	// Bodies is whether the caller asked for full text, which the text
	// reply follows for notes as well as bodies. The structured reply
	// carries notes whole either way.
	Bodies bool `json:"-"`
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
		Bodies:    opts.IncludeBody,
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
			Note:         task.Note,
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

// MaxTaskNoteBytes bounds a finish note. A note is what came of the work
// -- the outcome, a PR link, what is left -- and it rides the message to
// the task's creator, so it has to fit inside one with room for the rest.
const MaxTaskNoteBytes = 4000

// TaskFinish is a finish and what it set off: the dependents it made
// claimable and whether the task's creator was told.
type TaskFinish struct {
	Task Task `json:"task"`
	// Unblocked is read after the finish, so a dependent another session
	// claimed in between is left out: it is no longer anyone's to pick up.
	Unblocked []Task `json:"unblocked,omitempty" jsonschema:"dependents this finish made claimable: pending, with every dependency now done"`
	// Notified and NotNotified are exclusive; at most one is set.
	Notified    string `json:"notified,omitempty" jsonschema:"session id of the task's creator, which was sent a message saying the task is done"`
	NotNotified string `json:"not_notified,omitempty" jsonschema:"why the creator was not sent a message: you created the task, it predates recorded creators, or the creator is archived, gone or would not take the message"`
}

// FinishTask marks a claimed task done, keeps note on it, and tells the
// session that created it. The message is what closes the loop for a lead
// that split a plan into tasks: without it, the lead learns a piece landed
// only by polling the list, and the note is where the finisher says what
// came of it.
func (s *Sessions) FinishTask(sessionID, taskID, note string) (TaskFinish, error) {
	note = strings.TrimSpace(note)
	if len(note) > MaxTaskNoteBytes {
		return TaskFinish{}, fmt.Errorf("note is %d bytes, over the %d byte limit; say what came of the work and link the detail", len(note), MaxTaskNoteBytes)
	}
	runtime, caller, taskID, err := s.settling(sessionID, taskID)
	if err != nil {
		return TaskFinish{}, err
	}
	defer runtime.store.Close()
	changed, err := runtime.store.FinishTask(taskID, caller.ID, note, time.Now())
	if err != nil {
		return TaskFinish{}, err
	}
	if !changed {
		return TaskFinish{}, runtime.unsettled(caller.ID, taskID)
	}
	finished, err := runtime.task(caller.ID, taskID)
	if err != nil {
		return TaskFinish{}, err
	}
	result := TaskFinish{Task: finished}
	ready, err := runtime.store.ReadyDependents(taskID)
	if err != nil {
		return TaskFinish{}, err
	}
	if result.Unblocked, err = runtime.taskRows(caller.ID, ready); err != nil {
		return TaskFinish{}, err
	}
	// The finish has landed whatever happens to the message, so a message
	// that does not go is reported on the result rather than failing it:
	// a finisher retrying would find the task already done.
	result.Notified, result.NotNotified = runtime.notifyCreator(caller, result)
	return result, nil
}

func (s *Sessions) ReleaseTask(sessionID, taskID string) (Task, error) {
	runtime, caller, taskID, err := s.settling(sessionID, taskID)
	if err != nil {
		return Task{}, err
	}
	defer runtime.store.Close()
	changed, err := runtime.store.ReleaseTask(taskID, caller.ID, time.Now())
	if err != nil {
		return Task{}, err
	}
	if !changed {
		return Task{}, runtime.unsettled(caller.ID, taskID)
	}
	return runtime.task(caller.ID, taskID)
}

// settling opens the store for a finish or release; the caller closes it.
func (s *Sessions) settling(sessionID, taskID string) (*runtime, store.Session, string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, store.Session{}, "", fmt.Errorf("task_id is empty; call %s to get one", s.words.ListTasks)
	}
	runtime, err := s.open()
	if err != nil {
		return nil, store.Session{}, "", err
	}
	caller, err := runtime.caller(sessionID)
	if err != nil {
		runtime.store.Close()
		return nil, store.Session{}, "", err
	}
	return runtime, caller, taskID, nil
}

// unsettled says why a finish or release matched no row.
func (r *runtime) unsettled(callerID, taskID string) error {
	task, err := r.task(callerID, taskID)
	if err != nil {
		return err
	}
	if task.State == store.TaskInProgress && task.Owner != callerID {
		return fmt.Errorf("task %s is not yours to settle; %s holds it", taskID, holderOf(task))
	}
	return fmt.Errorf("task %s is %s, not in progress", taskID, task.State)
}

// notifyCreator sends the session that created a finished task a short
// message saying so. It returns the creator's id when the message was
// queued, or why none was.
//
// It goes through the same queue and checks as send_session, under the
// finisher's name, so the creator reads it as that session's words, which
// they are: the note is the finisher's. It is not the finisher answering
// the creator, though, so unlike a send it leaves the finisher's unread
// messages from the creator as they are.
func (r *runtime) notifyCreator(finisher store.Session, finish TaskFinish) (string, string) {
	task := finish.Task
	switch task.CreatedBy {
	case "":
		return "", "the task predates recorded creators"
	case finisher.ID:
		return "", "you created the task"
	}
	creator, err := r.store.Get(task.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "its creator " + task.CreatedBy + " is gone"
	}
	if err != nil {
		return "", "could not read its creator " + task.CreatedBy + ": " + err.Error()
	}
	if creator.Archived {
		return "", "its creator " + task.CreatedBy + " is archived"
	}
	from := sender{id: finisher.ID, name: finisher.Name}
	subject := "task " + task.ID + " finished"
	if _, err := r.enqueue(from, creator.ID, taskFinishedMessage(finisher, finish), subject, false); err != nil {
		logging.Info("could not tell a task's creator it is done",
			"task", task.ID, "creator", creator.ID, logging.Err(err))
		return "", "its creator " + creator.ID + " would not take the message: " + err.Error()
	}
	return creator.ID, ""
}

// maxNotifiedDependents bounds the dependents a finish message names; the
// rest are counted, since the creator can list them.
const maxNotifiedDependents = 10

func taskFinishedMessage(finisher store.Session, finish TaskFinish) string {
	task := finish.Task
	name := finisher.Name
	if name == "" {
		name = finisher.ID
	}
	lines := []string{fmt.Sprintf("Task %s you created is done: %s. Finished by %s (%s).",
		task.ID, oneLine(task.Title, 200), name, finisher.ID)}
	if task.Note != "" {
		lines = append(lines, "Note: "+task.Note)
	}
	if len(finish.Unblocked) > 0 {
		names := make([]string, 0, maxNotifiedDependents)
		for i, dep := range finish.Unblocked {
			if i == maxNotifiedDependents {
				names = append(names, fmt.Sprintf("and %d more", len(finish.Unblocked)-i))
				break
			}
			names = append(names, fmt.Sprintf("%s (%s)", oneLine(dep.Title, 200), dep.ID))
		}
		lines = append(lines, "Now claimable: "+strings.Join(names, "; ")+".")
	}
	return strings.Join(lines, "\n")
}

// oneLine flattens text to one line of at most limit bytes, cut on a rune
// boundary, with an ellipsis when it was cut.
func oneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
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
