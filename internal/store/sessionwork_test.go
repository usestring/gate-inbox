package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// Attached work survives the store being closed and reopened, a republish under the same id
// replaces the address and title in place, and the rows go with the session.
func TestSessionWorkPersistsReplacesOnReattachAndGoesWithTheSession(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(Session{ID: "s1", Name: "one", Tool: "claude", Cwd: "/repo", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	if err := st.AttachSessionWork("s1", []SessionWork{
		{Source: "pages", Kind: "artifact", ID: "p1", URL: "https://pages.example.test/p1", Title: "Draft"},
		{Source: "", Kind: "artifact", ID: "https://claude.ai/artifact/A1", URL: "https://claude.ai/artifact/A1"},
		{Source: "pages", Kind: "artifact", ID: "", URL: "https://pages.example.test/none"},
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	later := now.Add(time.Hour)
	if err := st.AttachSessionWork("s1", []SessionWork{
		{Source: "pages", Kind: "artifact", ID: "p1", URL: "https://pages.example.test/p1?v=2", Title: "Final"},
	}, later); err != nil {
		t.Fatal(err)
	}
	got, err := st.SessionWorkItems("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want the two items with an id", got)
	}
	// Both were first seen at the same instant, so the order falls to source: the board's first.
	if got[0].Source != "" || got[0].URL != "https://claude.ai/artifact/A1" {
		t.Errorf("first = %+v", got[0])
	}
	page := got[1]
	if page.URL != "https://pages.example.test/p1?v=2" || page.Title != "Final" {
		t.Errorf("a republish did not replace the page: %+v", page)
	}
	if !page.FirstSeen.Equal(now) || !page.UpdatedAt.Equal(later) {
		t.Errorf("first seen %v, updated %v; want %v, %v", page.FirstSeen, page.UpdatedAt, now, later)
	}
	if none, _ := st.SessionWorkItems("other"); len(none) != 0 {
		t.Errorf("another session sees %+v", none)
	}

	// A migration's new row inherits the work.
	if err := st.CreateSession(Session{ID: "s2", Name: "two", Tool: "claude", Cwd: "/repo", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteMigration("s1", "s2"); err != nil {
		t.Fatal(err)
	}
	if moved, _ := st.SessionWorkItems("s2"); len(moved) != 2 {
		t.Errorf("the migrated row has %+v", moved)
	}

	if err := st.Delete("s1"); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.SessionWorkItems("s1"); len(left) != 0 {
		t.Errorf("rows outlived the session: %+v", left)
	}
}
