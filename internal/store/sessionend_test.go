package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestAnEndIsKeptForTheLaunchItEnded(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	sess, err := st.Get("a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := st.RecordEnd(sess, EndKilled); err != nil {
		t.Fatalf("record: %v", err)
	}
	st.Close()

	// Another process -- the CLI, an MCP server -- wrote it; the board reads it.
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	row, err := reopened.Get("a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	ends, err := reopened.SessionEnds()
	if err != nil {
		t.Fatalf("ends: %v", err)
	}
	if end := ends["a"]; end.Reason != EndKilled || !end.Matches(row) {
		t.Fatalf("end = %+v, want a kill matching the row's launch", end)
	}

	// A revive moves the launch, and the mark no longer speaks for the agent.
	if err := reopened.SetAgentLaunchedAt("a", row.LaunchTime().Add(time.Hour)); err != nil {
		t.Fatalf("relaunch: %v", err)
	}
	if row, err = reopened.Get("a"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if ends["a"].Matches(row) {
		t.Fatal("a mark about the previous launch must not match the next one")
	}

	// A second end replaces the first rather than adding to it.
	if err := reopened.RecordEnd(row, EndArchived); err != nil {
		t.Fatalf("record again: %v", err)
	}
	if ends, _ = reopened.SessionEnds(); len(ends) != 1 || ends["a"].Reason != EndArchived || !ends["a"].Matches(row) {
		t.Fatalf("ends after a second end = %+v", ends)
	}

	if err := reopened.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ends, _ = reopened.SessionEnds(); len(ends) != 0 {
		t.Fatalf("a deleted row's end outlived it: %+v", ends)
	}
}

// close_terminal deletes through DeleteChild, which used to drop only the
// row: a terminal killed first left its end record, and anything else that
// named the id, pointing at an id a later session may be handed.
func TestDeleteChildClearsWhatDeleteClears(t *testing.T) {
	st := newTestStore(t)
	parent := sample("p", "g1")
	if err := st.CreateSession(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	shell := sample("sh", "g1")
	shell.ParentID = "p"
	if err := st.CreateSessionLeaf(shell); err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	row, err := st.Get("sh")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := st.RecordEnd(row, EndKilled); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, _, err := st.Enqueue(InboxMessage{SessionID: "p", SenderID: "sh", Body: "hi", SentAt: time.Now()}, DefaultInboxLimits); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := st.DeleteChild("sh", "p", func() error { return nil }); err != nil {
		t.Fatalf("DeleteChild: %v", err)
	}
	ends, err := st.SessionEnds()
	if err != nil {
		t.Fatalf("ends: %v", err)
	}
	if _, ok := ends["sh"]; ok {
		t.Fatal("the deleted terminal's end record is still stored")
	}
	var messages int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM session_inbox WHERE sender_id = 'sh' OR session_id = 'sh'`).Scan(&messages); err != nil {
		t.Fatalf("count: %v", err)
	}
	if messages != 0 {
		t.Fatalf("%d inbox rows still name the deleted terminal", messages)
	}
}
