package store

import (
	"path/filepath"
	"testing"
)

// Fast mode lives on the row, so a revive, a restart and a board restart
// launch the session in the mode it was started in.
func TestFastModeSurvivesTheRow(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(Session{ID: "fast0001", Name: "fast", Tool: "codex", FastMode: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(Session{ID: "plain001", Name: "plain", Tool: "claude"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("fast0001")
	if err != nil {
		t.Fatal(err)
	}
	if !got.FastMode {
		t.Errorf("Get = %+v, want fast mode kept", got)
	}
	listed, err := st.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, sess := range listed {
		if want := sess.ID == "fast0001"; sess.FastMode != want {
			t.Errorf("listed %s fast mode = %v, want %v", sess.ID, sess.FastMode, want)
		}
	}
}
