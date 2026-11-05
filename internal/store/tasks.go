// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package store

import (
	"database/sql"
	"errors"
	"fmt"
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
	if _, err := tx.Exec(
		`INSERT INTO tasks (id, title, body, owner_session_id, created_by, state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Title, task.Body, task.Owner, task.CreatedBy, task.State,
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
	return tx.Commit()
}

// ClaimTask takes a named task, refusing one already claimed or still
// blocked. SQLite serializes writers, so the guard and the write happen
// inside one write transaction and a second agent racing for the same
// task matches zero rows rather than overwriting the winner. An edge
// naming no task blocks too, which is what Blocking reports: a dependency
// nobody created is work that has not happened.
func (s *Store) ClaimTask(id, sessionID string, at time.Time) (bool, error) {
	res, err := s.db.Exec(`
UPDATE tasks SET state = ?, owner_session_id = ?, updated_at = ?
 WHERE id = ? AND state = ?
   AND NOT EXISTS (
     SELECT 1 FROM task_deps d LEFT JOIN tasks p ON p.id = d.depends_on_id
      WHERE d.task_id = tasks.id AND (p.id IS NULL OR p.state != ?)
   )`, TaskInProgress, sessionID, encodeTime(at), id, TaskPending, TaskDone)
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

// creatorFilter narrows alias t to tasks one of creators made. Nil
// creators is no narrowing, which is the global claim.
func creatorFilter(creators []string) (string, []any) {
	if creators == nil {
		return "", nil
	}
	if len(creators) == 0 {
		return " AND 0", nil
	}
	args := make([]any, 0, len(creators))
	for _, creator := range creators {
		args = append(args, creator)
	}
	return " AND t.created_by IN (" + placeholders(len(creators)) + ")", args
}

// ClaimNextTask takes the oldest task that is pending and unblocked, among
// the tasks one of creators made; nil creators takes from the whole list.
func (s *Store) ClaimNextTask(sessionID string, creators []string, at time.Time) (Task, bool, error) {
	task := Task{Owner: sessionID, State: TaskInProgress, UpdatedAt: at}
	var createdAt int64
	filter, filterArgs := creatorFilter(creators)
	args := []any{TaskInProgress, sessionID, encodeTime(at), TaskPending, TaskDone}
	args = append(args, filterArgs...)
	args = append(args, TaskPending)
	err := s.db.QueryRow(`
UPDATE tasks SET state = ?, owner_session_id = ?, updated_at = ?
 WHERE id = (
   SELECT t.id FROM tasks t
    WHERE `+readyTask+filter+`
    ORDER BY t.created_at, t.id LIMIT 1
 )
 AND state = ?
 RETURNING id, title, body, created_by, created_at`, args...).
		Scan(&task.ID, &task.Title, &task.Body, &task.CreatedBy, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	task.CreatedAt = decodeTime(createdAt)
	return task, true, nil
}

// ReadyTaskCount counts the tasks a global claim could take now, which is
// what a fleet-scoped claim that found nothing reports as lying outside
// the fleet.
func (s *Store) ReadyTaskCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT count(*) FROM tasks t WHERE `+readyTask, TaskPending, TaskDone).Scan(&count)
	return count, err
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

// DeleteTask drops a task and the dependency edges naming it. One
// transaction, because CreateTask is the only writer of task_deps: edges
// committed without the row they belong to are gone for good. The row goes
// first so an id naming no task leaves every edge where it is.
func (s *Store) DeleteTask(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if _, err := tx.Exec(`DELETE FROM task_deps WHERE task_id = ? OR depends_on_id = ?`, id, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
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
		if len(query.CreatedBy) == 0 {
			where = append(where, "0")
		} else {
			where = append(where, "created_by IN ("+placeholders(len(query.CreatedBy))+")")
			for _, creator := range query.CreatedBy {
				args = append(args, creator)
			}
		}
	}
	statement := `SELECT id, title, ` + body + `, owner_session_id, created_by, state, created_at, updated_at, count(*) OVER ()
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
		if err := rows.Scan(&task.ID, &task.Title, &task.Body, &task.Owner, &task.CreatedBy, &task.State, &createdAt, &updatedAt, &page.Matched); err != nil {
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
