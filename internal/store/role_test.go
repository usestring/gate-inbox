package store

import (
	"path/filepath"
	"testing"
)

func TestRoleIsStoredWithTheRowAndReadBackEverywhere(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(Session{ID: "par1", Name: "parent", Tool: "t", Cwd: "/"}); err != nil {
		t.Fatal(err)
	}
	if err := st.LaunchSessionLeaf(Session{ID: "help1", Name: "helper", Tool: "t", Cwd: "/", ParentID: "par1", Role: "ext1/reviewer"}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("help1")
	if err != nil || got.Role != "ext1/reviewer" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	listed, err := st.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{}
	for _, sess := range listed {
		roles[sess.ID] = sess.Role
	}
	if roles["help1"] != "ext1/reviewer" || roles["par1"] != "" {
		t.Fatalf("listed roles = %v", roles)
	}
}
