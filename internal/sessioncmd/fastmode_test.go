package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

// A child follows its parent's fast mode, may be started out of it, and may
// be started in it only by a parent that is in it.
func TestChildFastModeFollowsTheParent(t *testing.T) {
	yes, no := true, false
	fast := store.Session{FastMode: true}
	// Letting children skip permissions says nothing about fast mode.
	normal := store.Session{ChildrenSkipPermissions: true}
	for _, c := range []struct {
		name    string
		caller  store.Session
		asked   *bool
		want    bool
		refused bool
	}{
		{"unset follows a fast parent", fast, nil, true, false},
		{"unset follows a normal parent", normal, nil, false, false},
		{"false under a fast parent", fast, &no, false, false},
		{"true under a fast parent", fast, &yes, true, false},
		{"true under a normal parent", normal, &yes, false, true},
	} {
		got, err := childFastMode(c.caller, c.asked)
		if c.refused {
			if err == nil || !strings.Contains(err.Error(), "fast_mode") {
				t.Errorf("%s: err = %v, want a refusal", c.name, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

// An agent's move may keep or turn off fast mode, and turn it on only for a
// session it spawned while it is in fast mode itself. A session moving
// itself is no way into it.
func TestMigrateRaisesFastModeOnlyFromAFastSpawner(t *testing.T) {
	on, off := true, false
	parent := store.Session{ID: "parent01", FastMode: true}
	normalParent := store.Session{ID: "parent02", ChildrenSkipPermissions: true}
	stranger := store.Session{ID: "strange1", FastMode: true}
	child := store.Session{ID: "child001", SpawnedBy: "parent01"}
	childOfNormal := store.Session{ID: "child002", SpawnedBy: "parent02"}
	self := store.Session{ID: "self0001"}
	fast := store.Session{ID: "fast0001", FastMode: true}
	for _, c := range []struct {
		name    string
		caller  store.Session
		source  store.Session
		asked   *bool
		refused bool
	}{
		{"left alone", stranger, child, nil, false},
		{"turned off", stranger, fast, &off, false},
		{"kept on", stranger, fast, &on, false},
		{"raised by the fast spawner", parent, child, &on, false},
		{"raised by a normal spawner", normalParent, childOfNormal, &on, true},
		{"raised by another fast session", stranger, child, &on, true},
		{"raised by the session itself", self, self, &on, true},
	} {
		err := mayRaiseFastMode(c.caller, c.source, c.asked)
		if (err != nil) != c.refused {
			t.Errorf("%s: err = %v, refused want %v", c.name, err, c.refused)
		}
	}
}
