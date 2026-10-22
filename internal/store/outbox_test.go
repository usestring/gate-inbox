package store

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
)

func TestApplyOutboxMarksARestingRowWithAQueuedMessagePending(t *testing.T) {
	rows := []Session{
		{ID: "idle", Status: status.Idle},
		{ID: "finished", Status: status.Finished},
		{ID: "errored", Status: status.Errored},
		{ID: "empty", Status: status.Idle},
	}
	queued := map[string]int{"idle": 1, "finished": 2, "errored": 1}
	got := ApplyOutbox(rows, queued)
	for _, want := range []struct{ id, status string }{
		{"idle", status.Pending},
		{"finished", status.Pending},
		{"errored", status.Pending},
		{"empty", status.Idle},
	} {
		if s := statusOf(got, want.id); s != want.status {
			t.Errorf("%s = %q, want %q", want.id, s, want.status)
		}
	}
}

func TestApplyOutboxCountsAnUnsentLaunchPrompt(t *testing.T) {
	rows := []Session{
		{ID: "fresh", Status: status.Idle, PendingInputs: []string{"do the thing"}},
		{ID: "claimed", Status: status.Idle, PendingInputs: []string{"do the thing"}, PendingInputClaimed: true},
	}
	got := ApplyOutbox(rows, nil)
	if s := statusOf(got, "fresh"); s != status.Pending {
		t.Errorf("unsent launch prompt: %q, want pending", s)
	}
	// A claimed input is already on its way to the pane; the outbox does
	// not hold it.
	if s := statusOf(got, "claimed"); s != status.Idle {
		t.Errorf("claimed launch prompt: %q, want idle", s)
	}
}

func TestApplyOutboxLeavesBusyWaitingAndArchivedRowsAlone(t *testing.T) {
	rows := []Session{
		{ID: "working", Status: status.Working},
		{ID: "waiting", Status: status.Waiting},
		{ID: "starting", Status: status.Starting},
		{ID: "dead", Status: status.Dead},
		{ID: "archived", Status: status.Idle, Archived: true},
	}
	queued := map[string]int{"working": 1, "waiting": 1, "starting": 1, "dead": 1, "archived": 1}
	got := ApplyOutbox(rows, queued)
	for _, row := range rows {
		if s := statusOf(got, row.ID); s == status.Pending {
			t.Errorf("%s read pending", row.ID)
		}
	}
}
