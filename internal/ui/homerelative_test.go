package ui

import "testing"

func TestHomeRelativeShortensOnlyPathsUnderHome(t *testing.T) {
	t.Setenv("HOME", "/srv/op")
	for path, want := range map[string]string{
		"/srv/op":              "~",
		"/srv/op/repos/board":  "~/repos/board",
		"/srv/operator/elsewh": "/srv/operator/elsewh",
		"/tmp/demo":            "/tmp/demo",
		"":                     "",
	} {
		if got := homeRelative(path); got != want {
			t.Errorf("homeRelative(%q) = %q, want %q", path, got, want)
		}
	}
}
