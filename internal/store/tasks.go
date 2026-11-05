// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	TaskPending    = "pending"
	TaskInProgress = "in_progress"
	TaskDone       = "done"
)

// executor is the write half of database/sql, satisfied by both *sql.DB
// and *sql.Tx, so one statement can run inside a transaction or without.
type executor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// Task is one unit of shared work agents claim for themselves. The list
// is what lets a fleet coordinate without a lead agent brokering every
// handoff.
type Task struct {
	ID    string
	Title string
	Body  string
	Owner string
	// CreatedBy is the session that put the task on the list, empty on
	// rows written before anyone recorded it. It is what scopes a bare
	// claim to the caller's fleet.
	CreatedBy string
	// Assignee is the only session that may claim the task while it is
	// set; empty leaves it to anyone.
	Assignee  string
	State     string
	DependsOn []string
	// Blocking lists the dependencies that are not done, which is the part
	// of DependsOn worth telling anyone about: a finished dependency is in
	// nobody's way. An edge naming no task blocks too, since a dependency
	// nobody created is work that has not happened. Reads fill it in.
	Blocking  []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MissingDependencyError is CreateTask refusing an edge to a task that is
// not on the list, so the caller can name the id it got wrong.
type MissingDependencyError struct {
	ID string
}

func (e *MissingDependencyError) Error() string {
	return fmt.Sprintf("dependency %s does not exist", e.ID)
}

// CreateTask writes the row and its edges in one transaction, checking
// each dependency exists inside it. The row goes in first, which takes the
// write lock, so no DeleteTask can land between the check and the edge:
// an edge to a task deleted in that gap would name no task and block the
// new one for good, since nothing ever writes task_deps again.
func (s *Store) CreateTask(task Task) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertTask(tx, task); err != nil {
		return err
	}
	return tx.Commit()
}

func insertTask(tx *sql.Tx, task Task) error {
	if _, err := tx.Exec(
		`INSERT INTO tasks (id, title, body, owner_session_id, created_by, assignee, state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Title, task.Body, task.Owner, task.CreatedBy, task.Assignee, task.State,
		encodeTime(task.CreatedAt), encodeTime(task.UpdatedAt)); err != nil {
		return err
	}
	for _, dep := range task.DependsOn {
		var exists int
		err := tx.QueryRow(`SELECT count(*) FROM tasks WHERE id = ? AND id != ?`, dep, task.ID).Scan(&exists)
		if err != nil {
			return err
		}
		if exists == 0 {
			return &MissingDependencyError{ID: dep}
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO task_deps (task_id, depends_on_id) VALUES (?, ?)`,
			task.ID, dep); err != nil {
			return err
		}
	}
	return nil
}

// ClaimTask takes a named task, refusing one already claimed, still
// blocked, or assigned to another session. SQLite serializes writers, so
// the guard and the write happen inside one write transaction and a second
// agent racing for the same task matches zero rows rather than overwriting
// the winner. An edge naming no task blocks too, which is what Blocking
// reports: a dependency nobody created is work that has not happened.
func (s *Store) ClaimTask(id, sessionID string, at time.Time) (bool, error) {
	res, err := s.db.Exec(`
UPDATE tasks SET state = ?, owner_session_id = ?, updated_at = ?
 WHERE id = ? AND state = ? AND assignee IN ('', ?)
   AND NOT EXISTS (
     SELECT 1 FROM task_deps d LEFT JOIN tasks p ON p.id = d.depends_on_id
      WHERE d.task_id = tasks.id AND (p.id IS NULL OR p.state != ?)
   )`, TaskInProgress, sessionID, encodeTime(at), id, TaskPending, sessionID, TaskDone)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

// readyTask is the condition a claimable task meets, over alias t: pending
// and with every edge naming a finished task.
const readyTask = `t.state = ?
      AND NOT EXISTS (
        SELECT 1 FROM task_deps d LEFT JOIN tasks p ON p.id = d.depends_on_id
         WHERE d.task_id = t.id AND (p.id IS NULL OR p.state != ?)
      )`

// creatorIn is the condition that column names one of creators, for a
// caller that has already decided to narrow: an empty list matches
// nothing rather than everything.
func creatorIn(column string, creators []string) (string, []any) {
	if len(creators) == 0 {
		return "0", nil
	}
	args := make([]any, 0, len(creators))
	for _, creator := range creators {
		args = append(args, creator)
	}
	return column + " IN (" + placeholders(len(creators)) + ")", args
}

// ClaimNextTask takes the oldest task that is pending and unblocked: the
// ones assigned to sessionID first, whoever created them, then unassigned
// ones among the tasks one of creators made; nil creators takes unassigned
// work from the whole list. A task assigned to another session is never
// handed out.
func (s *Store) ClaimNextTask(sessionID string, creators []string, at time.Time) (Task, bool, error) {
	task := Task{Owner: sessionID, State: TaskInProgress, UpdatedAt: at}
	var createdAt int64
	args := []any{TaskInProgress, sessionID, encodeTime(at), TaskPending, TaskDone, sessionID}
	filter := ""
	if creators != nil {
		condition, creatorArgs := creatorIn("t.created_by", creators)
		filter = " AND " + condition
		args = append(args, creatorArgs...)
	}
	args = append(args, sessionID, TaskPending)
	err := s.db.QueryRow(`
UPDATE tasks SET state = ?, owner_session_id = ?, updated_at = ?
 WHERE id = (
   SELECT t.id FROM tasks t
    WHERE `+readyTask+`
      AND (t.assignee = ? OR (t.assignee = ''`+filter+`))
    ORDER BY t.assignee = ? DESC, t.created_at, t.id LIMIT 1
 )
 AND state = ?
 RETURNING id, title, body, created_by, assignee, created_at`, args...).
		Scan(&task.ID, &task.Title, &task.Body, &task.CreatedBy, &task.Assignee, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	task.CreatedAt = decodeTime(createdAt)
	return task, true, nil
}

// ReadyTaskCount counts the tasks a global claim by sessionID could take
// now, which is what a fleet-scoped claim that found nothing reports as
// lying outside the fleet. Work assigned to someone else is nobody's to
// suggest.
func (s *Store) ReadyTaskCount(sessionID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT count(*) FROM tasks t WHERE `+readyTask+` AND t.assignee IN ('', ?)`,
		TaskPending, TaskDone, sessionID).Scan(&count)
	return count, err
}

// mayAssign is who may move a pending task's assignee: its creator, its
// current assignee, or anyone at all when the task has neither, since a
// task from before creators were recorded has nobody whose plan an
// assignment could override.
const mayAssign = `(created_by = ? OR assignee = ? OR (created_by = '' AND assignee = ''))`

// AssignTask points a pending task at assignee, or at nobody when assignee
// is empty, on behalf of by. The guard is in the statement, so a claim or a
// rival assignment that lands first leaves this one matching no row.
func (s *Store) AssignTask(id, assignee, by string, at time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE tasks SET assignee = ?, updated_at = ?
		WHERE id = ? AND state = ? AND `+mayAssign,
		assignee, encodeTime(at), id, TaskPending, by, by)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

// RefusedAssignmentError is HandTasks finding a task it may not assign:
// gone, no longer pending, or someone else's to hand out.
type RefusedAssignmentError struct {
	ID string
}

func (e *RefusedAssignmentError) Error() string {
	return fmt.Sprintf("task %s cannot be assigned", e.ID)
}

// HandTasks assigns existing pending tasks and creates fresh ones for a
// session about to launch, in one transaction, so the new agent never
// starts holding half its list. It returns each existing task's previous
// assignee, which is what WithdrawHandedTasks puts back when the launch
// fails. An empty by is the operator's own spawn, which may assign any
// pending task.
func (s *Store) HandTasks(assignee, by string, existing []string, fresh []Task, at time.Time) (map[string]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	prior := make(map[string]string, len(existing))
	guard, guardArgs := " AND "+mayAssign, []any{by, by}
	if by == "" {
		guard, guardArgs = "", nil
	}
	for _, id := range existing {
		// Read, then compare-and-set on what was read: the update matches
		// no row if anyone moved the task in between.
		var previous string
		err := tx.QueryRow(`SELECT assignee FROM tasks WHERE id = ?`, id).Scan(&previous)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &RefusedAssignmentError{ID: id}
		}
		if err != nil {
			return nil, err
		}
		args := append([]any{assignee, encodeTime(at), id, TaskPending, previous}, guardArgs...)
		res, err := tx.Exec(`UPDATE tasks SET assignee = ?, updated_at = ?
			WHERE id = ? AND state = ? AND assignee = ?`+guard, args...)
		if err != nil {
			return nil, err
		}
		if affected, err := res.RowsAffected(); err != nil {
			return nil, err
		} else if affected == 0 {
			return nil, &RefusedAssignmentError{ID: id}
		}
		prior[id] = previous
	}
	for _, task := range fresh {
		task.Assignee = assignee
		if err := insertTask(tx, task); err != nil {
			return nil, err
		}
	}
	return prior, tx.Commit()
}

// WithdrawHandedTasks undoes HandTasks for a launch that failed: the fresh
// tasks go, and each existing one gets its previous assignee back unless
// somebody has moved it since.
func (s *Store) WithdrawHandedTasks(assignee string, fresh []string, prior map[string]string, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range fresh {
		res, err := tx.Exec(`DELETE FROM tasks WHERE id = ? AND assignee = ? AND state = ?`, id, assignee, TaskPending)
		if err != nil {
			return err
		}
		if affected, err := res.RowsAffected(); err != nil {
			return err
		} else if affected > 0 {
			if _, err := tx.Exec(`DELETE FROM task_deps WHERE task_id = ? OR depends_on_id = ?`, id, id); err != nil {
				return err
			}
		}
	}
	for id, previous := range prior {
		if _, err := tx.Exec(`UPDATE tasks SET assignee = ?, updated_at = ? WHERE id = ? AND assignee = ? AND state = ?`,
			previous, encodeTime(at), id, assignee, TaskPending); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FinishTask marks a claim done, which unblocks whatever depended on it.
func (s *Store) FinishTask(id, sessionID string, at time.Time) (bool, error) {
	return s.settleTask(id, sessionID, TaskDone, sessionID, at)
}

// ReleaseTask hands a claim back, so another agent can pick the work up.
func (s *Store) ReleaseTask(id, sessionID string, at time.Time) (bool, error) {
	return s.settleTask(id, sessionID, TaskPending, "", at)
}

func (s *Store) settleTask(id, sessionID, state, owner string, at time.Time) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE tasks SET state = ?, owner_session_id = ?, updated_at = ?
		  WHERE id = ? AND owner_session_id = ? AND state = ?`,
		state, owner, encodeTime(at), id, sessionID, TaskInProgress)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

// DeleteGuard is who is deleting a task and whether they asked to override
// the refusals.
type DeleteGuard struct {
	// CallerID is the deleting session; a task it holds itself is its own
	// to drop.
	CallerID string
	// Force deletes a task another session holds or is assigned, or one
	// unfinished work waits on.
	Force bool
}

// TaskDeletion is what a delete removed and what that set free.
type TaskDeletion struct {
	// Task is the row as it stood when it was deleted.
	Task Task
	// Unblocked is the dependents the delete made claimable. Its edges go
	// with it, since an edge naming no task would block them forever, so a
	// forced delete hands its dependents on as if it had been finished.
	Unblocked []Task
}

// DeleteRefusedError is a delete the guard stopped. HeldBy is set when
// another session holds the task, AssignedTo when it waits for another
// session to claim it, Dependents when unfinished tasks wait on it; any of
// them can be set alone.
type DeleteRefusedError struct {
	Task       Task
	HeldBy     string
	AssignedTo string
	Dependents []Task
}

func (e *DeleteRefusedError) Error() string {
	return fmt.Sprintf("task %s was not deleted: held by %q, assigned to %q, %d unfinished dependents",
		e.Task.ID, e.HeldBy, e.AssignedTo, len(e.Dependents))
}

// DeleteTask drops a task and the dependency edges naming it, unless the
// guard refuses it: a task another session holds is work in flight, one
// assigned to another session is work handed to it, and a task unfinished
// work waits on is a prerequisite that never happened, so all three take
// Force. The task's creator may drop what it assigned, since the plan the
// assignment carries out is its own. found is false when the id names no
// task.
//
// One transaction, because CreateTask is the only writer of task_deps:
// edges committed without the row they belong to are gone for good. The
// row goes first, which takes the write lock, so no claim or create can
// land between the guard reading the row and the row going; a refusal
// rolls it back. An id naming no task leaves every edge where it is.
func (s *Store) DeleteTask(id string, guard DeleteGuard) (deletion TaskDeletion, found bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return TaskDeletion{}, false, err
	}
	defer tx.Rollback()
	deleted, err := scanTaskRows(tx, `DELETE FROM tasks WHERE id = ?
 RETURNING id, title, '', owner_session_id, created_by, assignee, state, created_at, updated_at, 1`, id)
	if err != nil {
		return TaskDeletion{}, false, err
	}
	if len(deleted) == 0 {
		return TaskDeletion{}, false, nil
	}
	task := deleted[0]
	// A finished task blocks nobody, so only an unfinished one has
	// dependents worth guarding: removing an edge to done work changes no
	// one's state.
	var dependents []Task
	if task.State != TaskDone {
		if dependents, err = openDependents(tx, []string{id}); err != nil {
			return TaskDeletion{}, true, err
		}
	}
	if !guard.Force {
		refusal := &DeleteRefusedError{Task: task, Dependents: dependents}
		if task.State == TaskInProgress && task.Owner != guard.CallerID {
			refusal.HeldBy = task.Owner
		}
		if task.State == TaskPending && task.Assignee != "" &&
			task.Assignee != guard.CallerID && task.CreatedBy != guard.CallerID {
			refusal.AssignedTo = task.Assignee
		}
		if refusal.HeldBy != "" || refusal.AssignedTo != "" || len(refusal.Dependents) > 0 {
			return TaskDeletion{}, true, refusal
		}
	}
	unblocked, err := dropEdges(tx, []string{id}, dependents)
	if err != nil {
		return TaskDeletion{}, true, err
	}
	return TaskDeletion{Task: task, Unblocked: unblocked}, true, tx.Commit()
}

// openDependents is the tasks not done that wait on one of ids, excluding
// ids themselves.
func openDependents(tx *sql.Tx, ids []string) ([]Task, error) {
	args := make([]any, 0, 2*len(ids)+1)
	args = append(args, TaskDone)
	for _, id := range ids {
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	return scanTaskRows(tx, `
SELECT t.id, t.title, '', t.owner_session_id, t.created_by, t.assignee, t.state, t.created_at, t.updated_at, 1
  FROM tasks t
 WHERE t.state != ?
   AND EXISTS (SELECT 1 FROM task_deps d WHERE d.task_id = t.id AND d.depends_on_id IN (`+placeholders(len(ids))+`))
   AND t.id NOT IN (`+placeholders(len(ids))+`)
 ORDER BY t.created_at, t.id`, args...)
}

// dropEdges removes every edge naming ids, then reports which of
// dependents that left claimable.
func dropEdges(tx *sql.Tx, ids []string, dependents []Task) ([]Task, error) {
	args := make([]any, 0, 2*len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, args...)
	if _, err := tx.Exec(`DELETE FROM task_deps WHERE task_id IN (`+placeholders(len(ids))+`) OR depends_on_id IN (`+placeholders(len(ids))+`)`, args...); err != nil {
		return nil, err
	}
	if len(dependents) == 0 {
		return nil, nil
	}
	ready := []any{TaskPending, TaskDone}
	for _, dep := range dependents {
		ready = append(ready, dep.ID)
	}
	return scanTaskRows(tx, `
SELECT t.id, t.title, '', t.owner_session_id, t.created_by, t.assignee, t.state, t.created_at, t.updated_at, 1
  FROM tasks t
 WHERE `+readyTask+` AND t.id IN (`+placeholders(len(dependents))+`)
 ORDER BY t.created_at, t.id`, ready...)
}

// staleTask is the condition a task the prune sweeps meets, over alias t
// joined to its creator's session row as s: pending, created before the
// cutoff, put up by a session that can no longer speak for it -- archived,
// deleted, or never recorded -- and not assigned to a session still on the
// board, which is somebody the work was handed to. Archiving or deleting a
// session clears its assignments, so the check on the assignee's row only
// matters for one filed some other way.
const staleTask = `t.state = ? AND t.created_at < ?
   AND (t.created_by = '' OR s.id IS NULL OR s.archived != 0)
   AND NOT EXISTS (SELECT 1 FROM sessions a WHERE a.id = t.assignee AND a.archived = 0)`

// heldBack is the condition that keeps a stale task over alias t: a
// dependent somebody is working on.
const heldBack = `EXISTS (
     SELECT 1 FROM task_deps d JOIN tasks c ON c.id = d.task_id
      WHERE d.depends_on_id = t.id AND c.state = ?)`

// TaskPrune is what a prune swept, or would sweep on a dry run.
type TaskPrune struct {
	// Tasks is the stale tasks deleted, or on a dry run those that would be.
	Tasks []Task
	// Kept is the stale tasks left alone because a dependent is in progress.
	Kept []Task
	// Unblocked is the surviving dependents the prune made claimable; a dry
	// run leaves it empty.
	Unblocked []Task
}

// PruneTasks finds the pending tasks created before cutoff whose creator is
// archived, deleted or unrecorded -- work nobody is left to want -- and,
// unless dryRun, deletes them with their edges. A stale task with a
// dependent in progress is kept: that session is building on it.
//
// The delete runs first in its transaction, for the same reason as
// DeleteTask's: it takes the write lock before anything is read, so a claim
// cannot land between choosing a task and deleting it.
func (s *Store) PruneTasks(cutoff time.Time, dryRun bool) (TaskPrune, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return TaskPrune{}, err
	}
	defer tx.Rollback()
	candidates := `SELECT t.id FROM tasks t LEFT JOIN sessions s ON s.id = t.created_by
 WHERE ` + staleTask
	args := []any{TaskPending, encodeTime(cutoff)}
	var prune TaskPrune
	if dryRun {
		prune.Tasks, err = scanTaskRows(tx, `
SELECT t.id, t.title, '', t.owner_session_id, t.created_by, t.assignee, t.state, t.created_at, t.updated_at, 1
  FROM tasks t WHERE t.id IN (`+candidates+`) AND NOT `+heldBack+`
 ORDER BY t.created_at, t.id`, append(args, TaskInProgress)...)
	} else {
		prune.Tasks, err = scanTaskRows(tx, `DELETE FROM tasks AS t
 WHERE t.id IN (`+candidates+`) AND NOT `+heldBack+`
 RETURNING id, title, '', owner_session_id, created_by, assignee, state, created_at, updated_at, 1`, append(args, TaskInProgress)...)
	}
	if err != nil {
		return TaskPrune{}, err
	}
	// RETURNING comes back in no promised order.
	slices.SortFunc(prune.Tasks, func(a, b Task) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	if prune.Kept, err = scanTaskRows(tx, `
SELECT t.id, t.title, '', t.owner_session_id, t.created_by, t.assignee, t.state, t.created_at, t.updated_at, 1
  FROM tasks t WHERE t.id IN (`+candidates+`) AND `+heldBack+`
 ORDER BY t.created_at, t.id`, append(args, TaskInProgress)...); err != nil {
		return TaskPrune{}, err
	}
	if dryRun || len(prune.Tasks) == 0 {
		return prune, nil
	}
	ids := make([]string, 0, len(prune.Tasks))
	for _, task := range prune.Tasks {
		ids = append(ids, task.ID)
	}
	dependents, err := openDependents(tx, ids)
	if err != nil {
		return TaskPrune{}, err
	}
	if prune.Unblocked, err = dropEdges(tx, ids, dependents); err != nil {
		return TaskPrune{}, err
	}
	return prune, tx.Commit()
}

// scanTaskRows reads tasks in scanTasks' column order inside a transaction,
// closing the rows before returning so the next statement can run on the
// store's single connection. The edges are not loaded: these rows are
// reports, not reads of the list.
func scanTaskRows(tx *sql.Tx, statement string, args ...any) ([]Task, error) {
	rows, err := tx.Query(statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		var task Task
		var createdAt, updatedAt int64
		var ignored int
		if err := rows.Scan(&task.ID, &task.Title, &task.Body, &task.Owner, &task.CreatedBy, &task.Assignee, &task.State, &createdAt, &updatedAt, &ignored); err != nil {
			return nil, err
		}
		task.CreatedAt = decodeTime(createdAt)
		task.UpdatedAt = decodeTime(updatedAt)
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// TaskQuery narrows a read of the task list. The zero value is every task
// with its body, which is what a caller resolving one id wants; the list
// the tools hand out asks for less, and the store does the narrowing so a
// long-lived list of finished work is never read only to be thrown away.
type TaskQuery struct {
	// ID keeps only the task with this id.
	ID string
	// States keeps only tasks in these states; empty keeps every state.
	States []string
	// Owner keeps only tasks this session holds or finished.
	Owner string
	// CreatedBy keeps only tasks one of these sessions created; nil keeps
	// every creator, and an empty non-nil slice keeps none.
	CreatedBy []string
	// Limit caps the rows read, after filtering; zero reads them all.
	Limit int
	// SkipBody leaves each body empty. A body is the one unbounded column,
	// and a reader choosing what to claim needs the titles, not the
	// instructions.
	SkipBody bool
}

// TaskPage is the rows a query read plus how many matched it, so a caller
// can say when the limit left some out.
type TaskPage struct {
	Tasks   []Task
	Matched int
}

// Tasks reads the whole list, bodies included.
func (s *Store) Tasks() ([]Task, error) {
	page, err := s.QueryTasks(TaskQuery{})
	return page.Tasks, err
}

// QueryTasks reads the tasks matching query, oldest first. The count comes
// back from the same statement as the rows, as a window over the filtered
// set, so it counts what the limit cut rather than what it kept.
func (s *Store) QueryTasks(query TaskQuery) (TaskPage, error) {
	body := "body"
	if query.SkipBody {
		body = "''"
	}
	var where []string
	var args []any
	if query.ID != "" {
		where = append(where, "id = ?")
		args = append(args, query.ID)
	}
	if len(query.States) > 0 {
		where = append(where, "state IN ("+placeholders(len(query.States))+")")
		for _, state := range query.States {
			args = append(args, state)
		}
	}
	if query.Owner != "" {
		where = append(where, "owner_session_id = ?")
		args = append(args, query.Owner)
	}
	if query.CreatedBy != nil {
		condition, creatorArgs := creatorIn("created_by", query.CreatedBy)
		where = append(where, condition)
		args = append(args, creatorArgs...)
	}
	statement := `SELECT id, title, ` + body + `, owner_session_id, created_by, assignee, state, created_at, updated_at, count(*) OVER ()
  FROM tasks`
	if len(where) > 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	statement += " ORDER BY created_at, id"
	if query.Limit > 0 {
		statement += " LIMIT ?"
		args = append(args, query.Limit)
	}
	page, index, err := s.scanTasks(statement, args)
	if err != nil {
		return TaskPage{}, err
	}
	return page, s.loadTaskDeps(page.Tasks, index)
}

// scanTasks reads the rows in full and closes them before the edges are
// read: the store holds a single connection, so a second query cannot
// start while these rows are still open.
func (s *Store) scanTasks(statement string, args []any) (TaskPage, map[string]int, error) {
	rows, err := s.db.Query(statement, args...)
	if err != nil {
		return TaskPage{}, nil, err
	}
	defer rows.Close()
	page := TaskPage{Tasks: make([]Task, 0)}
	index := map[string]int{}
	for rows.Next() {
		var task Task
		var createdAt, updatedAt int64
		if err := rows.Scan(&task.ID, &task.Title, &task.Body, &task.Owner, &task.CreatedBy, &task.Assignee, &task.State, &createdAt, &updatedAt, &page.Matched); err != nil {
			return TaskPage{}, nil, err
		}
		task.CreatedAt = decodeTime(createdAt)
		task.UpdatedAt = decodeTime(updatedAt)
		index[task.ID] = len(page.Tasks)
		page.Tasks = append(page.Tasks, task)
	}
	return page, index, rows.Err()
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", count), ", ")
}

// loadTaskDeps fills in the edges of the tasks read, with the state of what
// each one names. The state comes from the join rather than from the rows
// already read, because a filtered read leaves out the finished tasks most
// dependencies point at, and a dependency missing from the page is not
// thereby unfinished.
func (s *Store) loadTaskDeps(tasks []Task, index map[string]int) error {
	if len(tasks) == 0 {
		return nil
	}
	args := make([]any, 0, len(tasks))
	for _, task := range tasks {
		args = append(args, task.ID)
	}
	rows, err := s.db.Query(`
SELECT d.task_id, d.depends_on_id, coalesce(p.state, '')
  FROM task_deps d LEFT JOIN tasks p ON p.id = d.depends_on_id
 WHERE d.task_id IN (`+placeholders(len(args))+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, dep, state string
		if err := rows.Scan(&id, &dep, &state); err != nil {
			return err
		}
		at, ok := index[id]
		if !ok {
			continue
		}
		tasks[at].DependsOn = append(tasks[at].DependsOn, dep)
		if state != TaskDone {
			tasks[at].Blocking = append(tasks[at].Blocking, dep)
		}
	}
	return rows.Err()
}

func (s *Store) Task(id string) (Task, error) {
	page, err := s.QueryTasks(TaskQuery{ID: id})
	if err != nil {
		return Task{}, err
	}
	if len(page.Tasks) == 0 {
		return Task{}, sql.ErrNoRows
	}
	return page.Tasks[0], nil
}

// ReleaseTasksOwnedBy hands back every claim a session held, so work does
// not sit in progress forever behind an agent that is gone.
func (s *Store) ReleaseTasksOwnedBy(sessionID string, at time.Time) error {
	return releaseTasksOwnedBy(s.db, sessionID, at)
}

// releaseTasksOwnedBy takes the executor because the deletion path runs it
// inside a transaction, and the store holds a single connection: a plain
// s.db.Exec issued while a Tx holds it waits forever.
func releaseTasksOwnedBy(exec executor, sessionID string, at time.Time) error {
	_, err := exec.Exec(
		`UPDATE tasks SET state = ?, owner_session_id = '', updated_at = ?
		  WHERE owner_session_id = ? AND state = ?`,
		TaskPending, encodeTime(at), sessionID, TaskInProgress)
	return err
}

// UnassignTasksOf clears sessionID from every task still assigned to it, so
// work handed to an agent that was archived or killed goes back to anyone
// rather than waiting on a session that will not claim it. A task it holds
// keeps its claim; only the reservation goes. Kept apart from the claim
// release beside it, because a kill on the way to a relaunch must keep the
// claim and the assignment both, and only the callers know which kill is
// which.
func (s *Store) UnassignTasksOf(sessionID string, at time.Time) error {
	return unassignTasksOf(s.db, sessionID, at)
}

func unassignTasksOf(exec executor, sessionID string, at time.Time) error {
	_, err := exec.Exec(`UPDATE tasks SET assignee = '', updated_at = ? WHERE assignee = ? AND state != ?`,
		encodeTime(at), sessionID, TaskDone)
	return err
}
