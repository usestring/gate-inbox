package store

import (
	"path/filepath"
	"testing"
)

// Both settings live on the row, so a revive, a restart and a board restart
// read back what the operator chose at launch.
func TestSkipPermissionsSurvivesTheRow(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(Session{ID: "skip0001", Name: "skipper", Tool: "codex", SkipPermissions: true, ChildrenSkipPermissions: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(Session{ID: "plain001", Name: "plain", Tool: "claude", ChildrenSkipPermissions: true}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("skip0001")
	if err != nil {
		t.Fatal(err)
	}
	if !got.SkipPermissions || !got.ChildrenSkipPermissions {
		t.Errorf("Get = %+v, want both settings kept", got)
	}
	listed, err := st.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, sess := range listed {
		want := sess.ID == "skip0001"
		if sess.SkipPermissions != want || !sess.ChildrenSkipPermissions {
			t.Errorf("listed %s = skip %v children %v", sess.ID, sess.SkipPermissions, sess.ChildrenSkipPermissions)
		}
	}
}
