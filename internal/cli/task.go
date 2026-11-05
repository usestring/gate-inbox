// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package cli

import (
	"fmt"
	"io"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

const (
	usageTaskList    = "task list [--state <state>] [--include-done] [--mine] [--created-by <me|session-id>] [--fleet] [--include-body] [--limit <n>] [--json]"
	usageTaskCreate  = "task create <title> [--body <text>] [--depends-on <id,id>] [--json]"
	usageTaskClaim   = "task claim [<task-id>] [--any] [--json]"
	usageTaskFinish  = "task finish <task-id> [--json]"
	usageTaskRelease = "task release <task-id> [--json]"
	usageTaskDelete  = "task delete <task-id> [--force] [--json]"
	usageTaskPrune   = "task prune [--older-than-days <n>] [--dry-run=false] [--json]"
)

type taskCommands interface {
	Tasks(sessionID string, opts sessioncmd.TaskListOptions) (sessioncmd.TaskList, error)
	CreateTask(sessionID, title, body string, dependsOn []string) (sessioncmd.Task, error)
	ClaimTask(sessionID, taskID string, opts sessioncmd.ClaimOptions) (sessioncmd.Task, error)
	FinishTask(sessionID, taskID string) (sessioncmd.Task, error)
	ReleaseTask(sessionID, taskID string) (sessioncmd.Task, error)
	DeleteTask(sessionID, taskID string, force bool) (sessioncmd.TaskDeletion, error)
	PruneTasks(sessionID string, opts sessioncmd.TaskPruneOptions) (sessioncmd.TaskPrune, error)
}

func newTasks(configDir string) taskCommands {
	return sessioncmd.NewSessions(configDir, sessioncmd.CLIVocabulary())
}

func taskVerbs() []command {
	return []command{
		{name: "list", usage: usageTaskList, about: "read the work list every session shares: what is pending, who holds what, and what is blocked", run: bind(newTasks, runTaskList)},
		{name: "create", usage: usageTaskCreate, about: "put a piece of work on the shared list so any session can pick it up", run: bind(newTasks, runTaskCreate)},
		{name: "claim", usage: usageTaskClaim, about: "take a task before you start it; omit the id to take the oldest pending task nothing is blocking that your fleet created, or pass --any for the whole list", run: bind(newTasks, runTaskClaim)},
		{name: "finish", usage: usageTaskFinish, about: "mark a task you claimed as done, which unblocks everything waiting on it", run: bind(newTasks, runTaskFinish)},
		{name: "release", usage: usageTaskRelease, about: "hand a task you claimed back to the list for another session", run: bind(newTasks, runTaskRelease)},
		{name: "delete", usage: usageTaskDelete, about: "remove a task nobody needs any more; refuses one another session holds or unfinished tasks depend on unless --force, which unblocks its dependents", run: bind(newTasks, runTaskDelete)},
		{name: "prune", usage: usageTaskPrune, about: "list the stale pending tasks whose creator is archived, deleted or unrecorded, and delete them with --dry-run=false", run: bind(newTasks, runTaskPrune)},
	}
}

func taskSection() section {
	return groupSection("Shared task list", "task", "the work list every session in this manager claims from", taskVerbs())
}

func runTaskList(out io.Writer, tasks taskCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageTaskList)
	var states stringList
	set.Var(&states, "state", "state to keep, repeatable or comma separated: pending, in_progress or done; omitted keeps pending and in_progress")
	includeDone := set.Bool("include-done", false, "also list finished tasks, which on a long-lived list are most of it")
	mine := set.Bool("mine", false, "keep only tasks this session holds or finished")
	createdBy := set.String("created-by", "", "keep only tasks this session created: me, or a session id")
	fleet := set.Bool("fleet", false, "keep only tasks your fleet created: you, your spawner or parent, and the sessions you spawned; the set a bare claim takes from")
	includeBody := set.Bool("include-body", false, "print each task's full instruction under its row")
	limit := set.Int("limit", sessioncmd.DefaultTaskLimit, fmt.Sprintf("how many rows to print, at most %d; the last line says what was left out", sessioncmd.MaxTaskLimit))
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	listed, err := tasks.Tasks(sessionID, sessioncmd.TaskListOptions{
		State:       states,
		IncludeDone: *includeDone,
		Mine:        *mine,
		CreatedBy:   *createdBy,
		Fleet:       *fleet,
		IncludeBody: *includeBody,
		Limit:       *limit,
	})
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, listed, sessioncmd.FormatTaskList(listed))
}

func runTaskCreate(out io.Writer, tasks taskCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageTaskCreate)
	body := set.String("body", "", "full instruction for whoever claims it; it cannot see your conversation")
	var dependsOn stringList
	set.Var(&dependsOn, "depends-on", "ids of tasks that must be done first, repeatable or comma separated")
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	created, err := tasks.CreateTask(sessionID, operands[0], *body, dependsOn)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, created, "created "+sessioncmd.FormatTask(created))
}

func runTaskClaim(out io.Writer, tasks taskCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageTaskClaim)
	anyCreator := set.Bool("any", false, "without a task id, take the oldest ready task on the whole list instead of only your fleet's")
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 0, 1)
	if err != nil {
		return err
	}
	taskID := ""
	if len(operands) == 1 {
		taskID = operands[0]
	}
	claimed, err := tasks.ClaimTask(sessionID, taskID, sessioncmd.ClaimOptions{Any: *anyCreator})
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, claimed, "claimed "+sessioncmd.FormatTask(claimed))
}

func runTaskFinish(out io.Writer, tasks taskCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageTaskFinish)
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	finished, err := tasks.FinishTask(sessionID, operands[0])
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, finished, "finished "+sessioncmd.FormatTask(finished))
}

func runTaskRelease(out io.Writer, tasks taskCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageTaskRelease)
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	released, err := tasks.ReleaseTask(sessionID, operands[0])
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, released, "released "+sessioncmd.FormatTask(released))
}

func runTaskDelete(out io.Writer, tasks taskCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageTaskDelete)
	force := set.Bool("force", false, "delete a task another session holds, or one unfinished tasks depend on; its dependents become claimable")
	asJSON := cmdline.JSONFlag(set)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	deleted, err := tasks.DeleteTask(sessionID, operands[0], *force)
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, deleted, sessioncmd.FormatTaskDeletion(deleted))
}

func runTaskPrune(out io.Writer, tasks taskCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageTaskPrune)
	days := set.Int("older-than-days", sessioncmd.DefaultPruneDays, "sweep only stale tasks created more than this many days ago")
	dryRun := set.Bool("dry-run", true, "list what would be deleted and delete nothing; --dry-run=false deletes it")
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	pruned, err := tasks.PruneTasks(sessionID, sessioncmd.TaskPruneOptions{OlderThanDays: *days, Delete: !*dryRun})
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, pruned, sessioncmd.FormatTaskPrune(pruned))
}
