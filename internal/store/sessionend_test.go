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
