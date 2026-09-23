package store

import (
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"path/filepath"
	"testing"
)

func TestAdoptedTmuxTargetSurvivesAnUnrelatedUpdate(t *testing.T) {
	st := newTestStore(t)
	sess := sample("a", "g1")
	sess.TmuxSocket = "default"
	sess.TmuxPaneID = "%12"
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.Get("a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TmuxSocket != "default" || got.TmuxPaneID != "%12" {
		t.Fatalf("after create: got socket %q pane %q", got.TmuxSocket, got.TmuxPaneID)
	}

	if err := st.RenameSession("a", "renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := st.UpdateStatus("a", "working"); err != nil {
		t.Fatalf("status: %v", err)
	}

	got, err = st.Get("a")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.TmuxSocket != "default" || got.TmuxPaneID != "%12" {
		t.Fatalf("after update: got socket %q pane %q", got.TmuxSocket, got.TmuxPaneID)
	}

	sessions, err := st.ListSessions(false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0].TmuxSocket != "default" || sessions[0].TmuxPaneID != "%12" {
		t.Fatalf("list: got socket %q pane %q", sessions[0].TmuxSocket, sessions[0].TmuxPaneID)
	}
}

func TestManagedSessionCarriesNoTmuxTarget(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.Get("a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TmuxSocket != "" || got.TmuxPaneID != "" {
		t.Fatalf("managed session should carry no target, got socket %q pane %q", got.TmuxSocket, got.TmuxPaneID)
	}
}

func TestSetTmuxTargetAdoptsAndReleases(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SetTmuxTarget("a", "other", "%7"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := st.Get("a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TmuxSocket != "other" || got.TmuxPaneID != "%7" {
		t.Fatalf("after set: got socket %q pane %q", got.TmuxSocket, got.TmuxPaneID)
	}
	if err := st.SetTmuxTarget("a", "", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err = st.Get("a")
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got.TmuxSocket != "" || got.TmuxPaneID != "" {
		t.Fatalf("after clear: got socket %q pane %q", got.TmuxSocket, got.TmuxPaneID)
	}
	if err := st.SetTmuxTarget("missing", "s", "%1"); err == nil {
		t.Fatal("want an error setting a target on a session that is gone")
	}
}

// TestOpenMigratesADatabaseWithoutTheTmuxColumns strips the columns back off a
// live database to reproduce a store written before this migration, so the
// additive ALTER is proven against real prior data rather than assumed.
func TestOpenMigratesADatabaseWithoutTheTmuxColumns(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE sessions DROP COLUMN tmux_socket`,
		`ALTER TABLE sessions DROP COLUMN tmux_pane_id`,
	} {
		if _, err := st.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
	got, err := reopened.Get("a")
	if err != nil {
		t.Fatalf("get after migration: %v", err)
	}
	if got.Name != "n-a" {
		t.Fatalf("want the pre-existing row back, got %+v", got)
	}
	if got.TmuxSocket != "" || got.TmuxPaneID != "" {
		t.Fatalf("migrated row should default to no target, got socket %q pane %q", got.TmuxSocket, got.TmuxPaneID)
	}
	if err := reopened.SetTmuxTarget("a", "default", "%3"); err != nil {
		t.Fatalf("set on migrated row: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened: %v", err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("third open: %v", err)
	}
	t.Cleanup(func() { again.Close() })
	got, err = again.Get("a")
	if err != nil {
		t.Fatalf("get after third open: %v", err)
	}
	if got.TmuxSocket != "default" || got.TmuxPaneID != "%3" {
		t.Fatalf("third open: got socket %q pane %q", got.TmuxSocket, got.TmuxPaneID)
	}
}
