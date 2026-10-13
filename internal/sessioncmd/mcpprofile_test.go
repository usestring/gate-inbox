package sessioncmd

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestMCPProfileGivesTrackedChildrenTheCoreTools(t *testing.T) {
	for _, c := range []struct {
		name string
		sess store.Session
		want string
	}{
		{"the operator's own session", store.Session{ID: "top00001"}, ""},
		{"a nested child", store.Session{ID: "kid00001", ParentID: "top00001", SpawnedBy: "top00001"}, mcpreg.ProfileCore},
		{"a child spawned by a child", store.Session{ID: "kid00002", ParentID: "top00001", SpawnedBy: "kid00001"}, mcpreg.ProfileCore},
		{"a detached spawn", store.Session{ID: "det00001", SpawnedBy: "top00001"}, ""},
		{"an extension's role", store.Session{ID: "run00001", ParentID: "top00001", Role: "charters/manager"}, ""},
	} {
		if got := mcpProfile(c.sess); got != c.want {
			t.Errorf("%s: profile %q, want %q", c.name, got, c.want)
		}
	}
}
