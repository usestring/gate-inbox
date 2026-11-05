package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// handedTasks is the work create_session assigns to the session it starts:
// tasks already on the list, and tasks it creates for the purpose.
type handedTasks struct {
	existing []store.Task
	fresh    []store.Task
}

func (h handedTasks) empty() bool {
	return len(h.existing) == 0 && len(h.fresh) == 0
}

// spawnTasks checks every task handed to a spawn before anything launches,
// so a list the caller may not hand out costs nothing to refuse. The store
// checks again when it writes, since a claim can land in between.
func (r *runtime) spawnTasks(caller store.Session, operator bool, specs []SpawnTask) (handedTasks, error) {
	var handed handedTasks
	seen := map[string]bool{}
	now := time.Now()
	for _, spec := range specs {
		id := strings.TrimSpace(spec.ID)
		if id == "" {
			task, err := newTask(TaskSpec{Title: spec.Title, Body: spec.Body, DependsOn: spec.DependsOn}, caller.ID, now)
			if err != nil {
				return handedTasks{}, fmt.Errorf("a task to create for the new session: %w; give it a title, or name an existing task by id", err)
			}
			handed.fresh = append(handed.fresh, task)
			continue
		}
		if strings.TrimSpace(spec.Title) != "" || strings.TrimSpace(spec.Body) != "" || len(spec.DependsOn) > 0 {
			return handedTasks{}, fmt.Errorf("task %s names an existing task and describes a new one; give either id or title, body and depends_on", id)
		}
		if seen[id] {
			return handedTasks{}, fmt.Errorf("task %s is listed twice", id)
		}
		seen[id] = true
		task, err := r.store.Task(id)
		if errors.Is(err, sql.ErrNoRows) {
			return handedTasks{}, fmt.Errorf("task %s does not exist; call %s for current ids", id, r.words.ListTasks)
		}
		if err != nil {
			return handedTasks{}, err
		}
		if task.State != store.TaskPending {
			return handedTasks{}, fmt.Errorf("task %s is %s; only a pending task can be assigned", id, task.State)
		}
		if !operator && task.CreatedBy != caller.ID && task.Assignee != caller.ID && (task.CreatedBy != "" || task.Assignee != "") {
			return handedTasks{}, r.refusedAssignment(caller.ID, id)
		}
		handed.existing = append(handed.existing, task)
	}
	return handed, nil
}

// appendTo adds the hand-off to the new agent's first prompt, spelled for
// the front its CLI reaches the manager through. An agent told only to
// "do the task" would not know the list exists, and one that never claims
// its tasks leaves them looking untouched to everyone else.
func (h handedTasks) appendTo(prompt string, words Vocabulary) string {
	if h.empty() {
		return prompt
	}
	lines := []string{"Tasks assigned to you on the shared task list (only you can claim them):"}
	for _, task := range append(append([]store.Task{}, h.existing...), h.fresh...) {
		lines = append(lines, fmt.Sprintf("- %s: %s", task.ID, task.Title))
	}
	lines = append(lines, fmt.Sprintf(
		"Claim each one with %s before you start it (with no task id, a claim takes these first), and mark it done with %s the moment it is finished.",
		words.ClaimTask, words.FinishTask))
	section := strings.Join(lines, "\n")
	if prompt == "" {
		return section
	}
	return prompt + "\n\n" + section
}

// handTasks writes the hand-off for the session about to launch as childID
// and returns what undoes it, for a launch that fails.
func (r *runtime) handTasks(childID, by string, handed handedTasks) (func(), error) {
	if handed.empty() {
		return func() {}, nil
	}
	ids := make([]string, 0, len(handed.existing))
	for _, task := range handed.existing {
		ids = append(ids, task.ID)
	}
	prior, err := r.store.HandTasks(childID, by, ids, handed.fresh, time.Now())
	var refused *store.RefusedAssignmentError
	if errors.As(err, &refused) {
		return nil, fmt.Errorf("task %s can no longer be assigned: it was claimed, deleted or reassigned since; call %s for its state", refused.ID, r.words.ListTasks)
	}
	if err != nil {
		return nil, r.missingDependency(err)
	}
	fresh := make([]string, 0, len(handed.fresh))
	for _, task := range handed.fresh {
		fresh = append(fresh, task.ID)
	}
	return func() {
		if err := r.store.WithdrawHandedTasks(childID, fresh, prior, time.Now()); err != nil {
			logging.Warn("could not take back the tasks handed to a spawn that failed", "session", childID, logging.Err(err))
		}
	}, nil
}
