package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

// A child follows the operator's setting for its parent's children, may turn
// its own prompts back on, and may not turn them off where the operator has
// not allowed it.
func TestChildSkipPermissionsFollowsTheParentsChildrenSetting(t *testing.T) {
	yes, no := true, false
	allowed := store.Session{ChildrenSkipPermissions: true}
	// A parent that itself skips permissions says nothing about its children.
	withheld := store.Session{SkipPermissions: true}
	for _, c := range []struct {
		name      string
		caller    store.Session
		asked     *bool
		supported bool
		want      bool
		refused   bool
	}{
		{"unset follows an allowing parent", allowed, nil, true, true, false},
		{"unset follows a withholding parent", withheld, nil, true, false, false},
		{"unset keeps prompts on a CLI with no flag", allowed, nil, false, false, false},
		{"false narrows an allowing parent", allowed, &no, true, false, false},
		{"true under an allowing parent", allowed, &yes, true, true, false},
		{"true under a withholding parent", withheld, &yes, true, false, true},
	} {
		got, err := childSkipPermissions(c.caller, c.asked, c.supported)
		if c.refused {
			if err == nil || !strings.Contains(err.Error(), "skip_permissions") {
				t.Errorf("%s: err = %v, want a refusal", c.name, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

// An agent's move may keep or turn off either setting, and turn one on only
// for a child it spawned while the operator lets its children skip.
func TestMigrateRaisesSkipPermissionsOnlyWhereAllowed(t *testing.T) {
	on, off := true, false
	parent := store.Session{ID: "parent01", ChildrenSkipPermissions: true}
	stranger := store.Session{ID: "strange1"}
	child := store.Session{ID: "child001", SpawnedBy: "parent01"}
	skipping := store.Session{ID: "skipper1", SkipPermissions: true, ChildrenSkipPermissions: true}
	for _, c := range []struct {
		name    string
		caller  store.Session
		source  store.Session
		opts    MigrateOptions
		refused bool
	}{
		{"left alone", stranger, child, MigrateOptions{}, false},
		{"turned off", stranger, skipping, MigrateOptions{SkipPermissions: &off, ChildrenSkipPermissions: &off}, false},
		{"kept on", stranger, skipping, MigrateOptions{SkipPermissions: &on, ChildrenSkipPermissions: &on}, false},
		{"raised by the allowing spawner", parent, child, MigrateOptions{SkipPermissions: &on, ChildrenSkipPermissions: &on}, false},
		{"raised by anyone else", stranger, child, MigrateOptions{SkipPermissions: &on}, true},
		{"children raised by anyone else", stranger, child, MigrateOptions{ChildrenSkipPermissions: &on}, true},
	} {
		err := mayRaiseSkipPermissions(c.caller, c.source, c.opts)
		if (err != nil) != c.refused {
			t.Errorf("%s: err = %v, refused want %v", c.name, err, c.refused)
		}
	}
}
