package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

// creatorInbox is what the caller, the harness's one session with a pane,
// has been sent so far.
func (h *sessionHarness) creatorInbox(t *testing.T) []store.InboxMessage {
	t.Helper()
	messages, err := h.store.Inbox(h.caller.ID, store.InboxFilter{Limit: 20})
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	return messages
}

func TestFinishKeepsTheNoteAndTellsTheCreatorWhatItUnblocked(t *testing.T) {
	h := newSessionHarness(t)
	worker := h.addChildRow(t, "retry-worker", h.caller.ID)

	first, err := h.sessions.CreateTask(h.caller.ID, "add the retry column", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	other, err := h.sessions.CreateTask(h.caller.ID, "write the dashboard", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	ready, err := h.sessions.CreateTask(h.caller.ID, "backfill retries", "", []string{first.ID})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Waits on first and on other: finishing first leaves it blocked, so
	// it is no news to the creator.
	stillBlocked, err := h.sessions.CreateTask(h.caller.ID, "chart retries", "", []string{first.ID, other.ID})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if _, err := h.sessions.ClaimTask(worker, first.ID, ClaimOptions{}); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	note := "merged https://github.com/usestring/gate-inbox/pull/1; the index is left for later"
	finished, err := h.sessions.FinishTask(worker, first.ID, "  "+note+"\n")
	if err != nil {
		t.Fatalf("FinishTask: %v", err)
	}
	if finished.Task.State != store.TaskDone || finished.Task.Note != note {
		t.Fatalf("finished = %+v, want done with the trimmed note", finished.Task)
	}
	if len(finished.Unblocked) != 1 || finished.Unblocked[0].ID != ready.ID {
		t.Fatalf("unblocked = %+v, want only %s", finished.Unblocked, ready.ID)
	}
	if finished.Notified != h.caller.ID || finished.NotNotified != "" {
		t.Fatalf("notified = %q, not notified = %q; want the creator told", finished.Notified, finished.NotNotified)
	}

	messages := h.creatorInbox(t)
	if len(messages) != 1 {
		t.Fatalf("creator inbox = %+v, want one message", messages)
	}
	msg := messages[0]
	if msg.SenderID != worker || msg.SenderName != "retry-worker" {
		t.Fatalf("message from %q (%q), want the finisher", msg.SenderID, msg.SenderName)
	}
	for _, want := range []string{first.ID, "add the retry column", "retry-worker", note, "backfill retries (" + ready.ID + ")"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("message %q is missing %q", msg.Body, want)
		}
	}
	if strings.Contains(msg.Body, stillBlocked.ID) {
		t.Errorf("message %q names %s, which is still blocked", msg.Body, stillBlocked.ID)
	}

	// The note is read back on the list, which is where anyone but the
	// creator finds it.
	listed, err := h.sessions.Tasks(h.caller.ID, TaskListOptions{State: []string{store.TaskDone}})
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(listed.Tasks) != 1 || listed.Tasks[0].Note != note {
		t.Fatalf("listed done tasks = %+v", listed.Tasks)
	}
}

func TestFinishTellsNoCreatorThatCannotUseIt(t *testing.T) {
	h := newSessionHarness(t)
	worker := h.addChildRow(t, "worker", h.caller.ID)
	finishAs := func(t *testing.T, creator, finisher string) TaskFinish {
		t.Helper()
		created, err := h.sessions.CreateTask(creator, "a piece of work", "", nil)
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := h.sessions.ClaimTask(finisher, created.ID, ClaimOptions{}); err != nil {
			t.Fatalf("ClaimTask: %v", err)
		}
		finished, err := h.sessions.FinishTask(finisher, created.ID, "done")
		if err != nil {
			t.Fatalf("FinishTask: %v", err)
		}
		if finished.Task.State != store.TaskDone || finished.Notified != "" {
			t.Fatalf("finished = %+v, want done with nobody told", finished)
		}
		return finished
	}

	t.Run("the finisher created it", func(t *testing.T) {
		if got := finishAs(t, h.caller.ID, h.caller.ID).NotNotified; got != "you created the task" {
			t.Fatalf("not notified = %q", got)
		}
	})
	t.Run("the creator is archived", func(t *testing.T) {
		creator := h.addSessionRow(t, "filed-lead")
		finished := func() TaskFinish {
			created, err := h.sessions.CreateTask(creator, "filed work", "", nil)
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			if err := h.store.SetArchived(creator, true); err != nil {
				t.Fatalf("SetArchived: %v", err)
			}
			if _, err := h.sessions.ClaimTask(worker, created.ID, ClaimOptions{}); err != nil {
				t.Fatalf("ClaimTask: %v", err)
			}
			finished, err := h.sessions.FinishTask(worker, created.ID, "")
			if err != nil {
				t.Fatalf("FinishTask: %v", err)
			}
			return finished
		}()
		if !strings.Contains(finished.NotNotified, "is archived") || finished.Notified != "" {
			t.Fatalf("finished = %+v, want the archived creator left alone", finished)
		}
		// Archiving is not a reason to bring the session back: a send from
		// its spawner would restore it, and this is no such send.
		if sess, err := h.store.Get(creator); err != nil || !sess.Archived {
			t.Fatalf("creator after finish = %+v, %v; want still archived", sess, err)
		}
	})
	t.Run("the creator is gone", func(t *testing.T) {
		creator := h.addSessionRow(t, "gone-lead")
		created, err := h.sessions.CreateTask(creator, "orphaned work", "", nil)
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if err := h.store.Delete(creator); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := h.sessions.ClaimTask(worker, created.ID, ClaimOptions{Any: true}); err != nil {
			t.Fatalf("ClaimTask: %v", err)
		}
		finished, err := h.sessions.FinishTask(worker, created.ID, "")
		if err != nil {
			t.Fatalf("FinishTask: %v", err)
		}
		if !strings.Contains(finished.NotNotified, "is gone") {
			t.Fatalf("not notified = %q", finished.NotNotified)
		}
	})
	t.Run("the creator will not take a message", func(t *testing.T) {
		// A row with no pane is not running, so the queue refuses it; the
		// finish has landed all the same.
		creator := h.addSessionRow(t, "paneless-lead")
		if got := finishAs(t, creator, worker).NotNotified; !strings.Contains(got, "would not take the message") {
			t.Fatalf("not notified = %q", got)
		}
	})
	if messages := h.creatorInbox(t); len(messages) != 0 {
		t.Fatalf("caller inbox = %+v, want nothing sent", messages)
	}
}

func TestFinishRefusesAnOversizedNoteBeforeSettling(t *testing.T) {
	h := newSessionHarness(t)
	created, err := h.sessions.CreateTask(h.caller.ID, "a piece of work", "", nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := h.sessions.ClaimTask(h.caller.ID, created.ID, ClaimOptions{}); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if _, err := h.sessions.FinishTask(h.caller.ID, created.ID, strings.Repeat("x", MaxTaskNoteBytes+1)); err == nil ||
		!strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized note = %v", err)
	}
	task, err := h.store.Task(created.ID)
	if err != nil || task.State != store.TaskInProgress {
		t.Fatalf("task after refusal = %+v, %v; want still in progress", task, err)
	}
}

func TestTaskFinishedMessageFitsTheQueue(t *testing.T) {
	finish := TaskFinish{Task: Task{ID: "t1", Title: strings.Repeat("title ", 100), Note: strings.Repeat("n", MaxTaskNoteBytes)}}
	for i := 0; i < 40; i++ {
		finish.Unblocked = append(finish.Unblocked, Task{ID: "dep", Title: strings.Repeat("dependent ", 40)})
	}
	msg := taskFinishedMessage(store.Session{ID: "w1", Name: "worker"}, finish)
	if len(msg) > maxMessageBytes {
		t.Fatalf("message is %d bytes, over the %d the queue takes", len(msg), maxMessageBytes)
	}
	if !strings.Contains(msg, "and 30 more") {
		t.Fatalf("message %q does not count the dependents it left out", msg)
	}
}

func TestFormatTaskShortensTheNoteAndAListWithBodiesCarriesItWhole(t *testing.T) {
	note := strings.Repeat("long outcome ", 40)
	task := Task{ID: "t1", Title: "work", State: store.TaskDone, Note: strings.TrimSpace(note)}
	row := FormatTask(task)
	if !strings.Contains(row, "; note: long outcome") || strings.Contains(row, task.Note) {
		t.Fatalf("row = %q, want a shortened note", row)
	}
	if text := FormatTaskList(TaskList{Tasks: []Task{task}, Returned: 1, Matched: 1}); strings.Contains(text, task.Note) {
		t.Fatalf("list without bodies = %q, want the note shortened", text)
	}
	if text := FormatTaskList(TaskList{Tasks: []Task{task}, Returned: 1, Matched: 1, Bodies: true}); !strings.Contains(text, task.Note) {
		t.Fatalf("list with bodies = %q, want the note whole", text)
	}
	finished := FormatTaskFinish(TaskFinish{Task: task, NotNotified: "you created the task"})
	if !strings.Contains(finished, "note: "+task.Note) || !strings.Contains(finished, "did not tell its creator: you created the task") {
		t.Fatalf("finish text = %q", finished)
	}
}
