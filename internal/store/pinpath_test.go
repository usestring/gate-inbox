package store

import (
	"errors"
	"testing"
)

// A group's pin round-trips, covers the subtree below it, and yields to a
// nearer pin; a pin on a group with no path pins nothing.
func TestPinnedPathIsTheNearestPinnedGroupsPath(t *testing.T) {
	st := newTestStore(t)
	st.CreateGroup("proj", "/repo")
	st.CreateGroup("proj/api", "/repo/api")
	st.CreateGroup("proj/api/auth", "")
	st.CreateGroup("proj/web", "/repo/web")
	st.CreateGroup("bare", "")

	groups, _ := st.Groups()
	if got := PinnedPath(groups, "proj/api"); got != "" {
		t.Fatalf("nothing pinned yet, got %q", got)
	}
	if err := st.SetGroupPinPath("proj", true); err != nil {
		t.Fatalf("pin proj: %v", err)
	}
	if err := st.SetGroupPinPath("bare", true); err != nil {
		t.Fatalf("pin bare: %v", err)
	}
	groups, _ = st.Groups()
	for _, g := range groups {
		if want := g.Name == "proj" || g.Name == "bare"; g.PinPath != want {
			t.Fatalf("group %s pinned = %v, want %v", g.Name, g.PinPath, want)
		}
	}
	for group, want := range map[string]string{
		"proj":          "/repo",
		"proj/api":      "/repo",
		"proj/api/auth": "/repo",
		"proj/web":      "/repo",
		"bare":          "",
		"":              "",
	} {
		if got := PinnedPath(groups, group); got != want {
			t.Fatalf("PinnedPath(%q) = %q, want %q", group, got, want)
		}
	}

	if err := st.SetGroupPinPath("proj/api", true); err != nil {
		t.Fatalf("pin proj/api: %v", err)
	}
	groups, _ = st.Groups()
	if got := PinnedPath(groups, "proj/api/auth"); got != "/repo/api" {
		t.Fatalf("the nearer pin should win, got %q", got)
	}

	if err := st.SetGroupPinPath("proj", false); err != nil {
		t.Fatalf("unpin proj: %v", err)
	}
	groups, _ = st.Groups()
	if got := PinnedPath(groups, "proj/web"); got != "" {
		t.Fatalf("an unpinned group still pins, got %q", got)
	}
	if err := st.SetGroupPinPath("missing", true); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("pinning a missing group = %v, want ErrGroupNotFound", err)
	}
	if err := st.SetGroupPinPath("", true); err == nil {
		t.Fatal("pinning the root group should be refused")
	}
}
