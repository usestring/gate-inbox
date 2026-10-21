package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
)

// A board built before relaunches restored their rows revived an archived
// session and left the row archived. The pass derives nothing for an
// archived row, so the running agent read "starting" for good and the
// active list never showed it. The next pass must put it back.
func TestPassRestoresAnArchivedRowWithALivePane(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "revived", t.TempDir(), "")
	createSession(t, m, "filed", t.TempDir(), "")
	var live, dead string
	for _, row := range m.sessionRows() {
		switch row.Name {
		case "revived":
			live = row.ID
		case "filed":
			dead = row.ID
		}
	}
	if err := m.tmux.Kill(dead); err != nil {
		t.Fatalf("kill: %v", err)
	}
	for _, id := range []string{live, dead} {
		if err := m.store.UpdateStatus(id, status.Starting); err != nil {
			t.Fatal(err)
		}
		if err := m.store.SetArchived(id, true); err != nil {
			t.Fatal(err)
		}
	}

	if msg, failed := m.poller.refreshOnce().(errMsg); failed {
		t.Fatalf("refresh: %v", msg.err)
	}

	row, err := m.store.Get(live)
	if err != nil {
		t.Fatal(err)
	}
	if row.Archived {
		t.Fatal("an archived row whose pane is running stayed archived")
	}
	if row.Status == status.Dead {
		t.Fatalf("the restored row reads %q with its pane running", row.Status)
	}
	if row, err = m.store.Get(dead); err != nil {
		t.Fatal(err)
	}
	if !row.Archived {
		t.Fatal("an archived row with no pane was restored")
	}
}
