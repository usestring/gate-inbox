package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// An assigned task is work its creator meant for one session: anyone else
// claiming it by id is told whose it is, and the assignee's bare claim
// takes it ahead of older unassigned work.
func TestAnAssignedTaskIsClaimedOnlyByItsAssignee(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	lead := h.caller.ID
	worker := h.addChildRow(t, "worker", lead)
	rival := h.addChildRow(t, "rival", lead)
	older, err := h.sessions.CreateTask(lead, TaskSpec{Title: "older open work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	handed, err := h.sessions.CreateTask(lead, TaskSpec{Title: "the worker's job", Assignee: worker})
	if err != nil {
		t.Fatalf("CreateTask assigned: %v", err)
	}
	if handed.Assignee != worker || handed.AssigneeName != "worker" {
		t.Fatalf("created task = %+v, want it assigned to the worker", handed)
	}
	if text := FormatTask(handed); !strings.Contains(text, "assigned to worker") {
		t.Fatalf("the task text does not name its assignee: %q", text)
	}

	if _, err := h.sessions.ClaimTask(rival, handed.ID, ClaimOptions{}); err == nil ||
		!strings.Contains(err.Error(), "is assigned to worker") {
		t.Fatalf("a rival's claim by id = %v, want it refused with the assignee named", err)
	}
	// The rival's bare claim skips the worker's task and takes the open one.
	claimed, err := h.sessions.ClaimTask(rival, "", ClaimOptions{Any: true})
	if err != nil || claimed.ID != older.ID {
		t.Fatalf("the rival's bare claim = %+v, %v; want the unassigned task", claimed, err)
	}
	if _, err := h.sessions.ReleaseTask(rival, older.ID); err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}
	claimed, err = h.sessions.ClaimTask(worker, "", ClaimOptions{})
	if err != nil || claimed.ID != handed.ID {
		t.Fatalf("the worker's bare claim = %+v, %v; want its assigned task before the older one", claimed, err)
	}
}

// The creator or the current assignee moves an assignment; anyone else is
// told who can.
func TestOnlyTheCreatorOrAssigneeMovesAnAssignment(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	lead := h.caller.ID
	worker := h.addChildRow(t, "worker", lead)
	stranger := h.addSessionRow(t, "stranger")
	task, err := h.sessions.CreateTask(lead, TaskSpec{Title: "planned work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := h.sessions.AssignTask(stranger, task.ID, "me"); err == nil ||
		!strings.Contains(err.Error(), "only its creator "+h.caller.Name+" can move it") {
		t.Fatalf("a stranger's assignment = %v", err)
	}
	if _, err := h.sessions.AssignTask(lead, task.ID, "nope1234"); err == nil ||
		!strings.Contains(err.Error(), "is not a session") {
		t.Fatalf("assigning to no session = %v", err)
	}
	assigned, err := h.sessions.AssignTask(lead, task.ID, worker)
	if err != nil || assigned.Assignee != worker {
		t.Fatalf("the creator's assignment = %+v, %v", assigned, err)
	}
	// The assignee can hand it on, or back.
	unassigned, err := h.sessions.AssignTask(worker, task.ID, "")
	if err != nil || unassigned.Assignee != "" {
		t.Fatalf("the assignee unassigning = %+v, %v", unassigned, err)
	}
	if _, err := h.sessions.ClaimTask(lead, task.ID, ClaimOptions{}); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if _, err := h.sessions.AssignTask(lead, task.ID, worker); err == nil ||
		!strings.Contains(err.Error(), "only a pending task can be assigned") {
		t.Fatalf("assigning a claimed task = %v", err)
	}
}

// create_session's tasks are assigned to the child before it launches and
// listed in its prompt; killing it hands the ones it never claimed back to
// anyone.
func TestASpawnIsHandedItsTasksAndAKillHandsThemBack(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	existing, err := h.sessions.CreateTask(h.caller.ID, TaskSpec{Title: "wire the column"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	child, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{
		Tool: "resting", Name: "worker", Prompt: "you own the column work",
		Tasks: []SpawnTask{{ID: existing.ID}, {Title: "backfill the column", Body: "every row", DependsOn: []string{existing.ID}}},
	})
	if err != nil {
		t.Fatalf("Create with tasks: %v", err)
	}
	listed, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{IncludeBody: true})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(listed.Tasks) != 2 {
		t.Fatalf("tasks after the spawn = %+v", listed.Tasks)
	}
	row, err := h.store.Get(child.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	prompt := row.LaunchPrompt + strings.Join(row.PendingInputs, "\n")
	if !strings.Contains(prompt, "you own the column work") {
		t.Fatalf("the brief was lost:\n%s", prompt)
	}
	for _, task := range listed.Tasks {
		if task.Assignee != child.ID || task.CreatedBy != h.caller.ID {
			t.Fatalf("%s after the spawn = %+v, want it created by the caller and assigned to the child", task.Title, task)
		}
		if !strings.Contains(prompt, "- "+task.ID+": "+task.Title) {
			t.Fatalf("the child's prompt does not list %s:\n%s", task.ID, prompt)
		}
		if task.Title == "backfill the column" && (task.Body != "every row" || len(task.DependsOn) != 1) {
			t.Fatalf("the created task lost its body or dependency: %+v", task)
		}
	}
	// Spelled for the child's front: this test CLI has no MCP client, so
	// the prompt names the subcommands.
	if !strings.Contains(prompt, CLIVocabulary().ClaimTask+" before") || !strings.Contains(prompt, CLIVocabulary().FinishTask+" the moment") {
		t.Fatalf("the child's prompt does not say how to claim and finish:\n%s", prompt)
	}

	if _, err := h.sessions.Kill(h.caller.ID, child.ID, extension.KillByCLI); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	listed, err = h.sessions.Tasks(h.caller.ID, TaskListOptions{})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	for _, task := range listed.Tasks {
		if task.Assignee != "" {
			t.Fatalf("%s is still assigned to the killed child: %+v", task.Title, task)
		}
	}
}

// A spawn handed a task its caller may not assign launches nothing and
// creates nothing.
func TestASpawnWithATaskItMayNotAssignIsRefusedWhole(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	stranger := h.addSessionRow(t, "stranger")
	theirs, err := h.sessions.CreateTask(stranger, TaskSpec{Title: "the stranger's work"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	_, err = h.sessions.Create(h.caller.ID, CreateSessionOptions{
		Tool: "resting", Name: "worker",
		Tasks: []SpawnTask{{Title: "fresh work"}, {ID: theirs.ID}},
	})
	if err == nil || !strings.Contains(err.Error(), "not yours to assign") {
		t.Fatalf("a spawn with another session's task = %v", err)
	}
	listed, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(listed.Tasks) != 1 || listed.Tasks[0].ID != theirs.ID || listed.Tasks[0].Assignee != "" {
		t.Fatalf("a refused spawn changed the list: %+v", listed.Tasks)
	}
	for _, bad := range []SpawnTask{{}, {ID: theirs.ID, Title: "both"}} {
		if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker", Tasks: []SpawnTask{bad}}); err == nil {
			t.Fatalf("a spawn task %+v was accepted", bad)
		}
	}
}
