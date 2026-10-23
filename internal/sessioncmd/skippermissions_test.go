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
		name    string
		caller  store.Session
		asked   *bool
		want    bool
		refused bool
	}{
		{"unset follows an allowing parent", allowed, nil, true, false},
		{"unset follows a withholding parent", withheld, nil, false, false},
		{"false narrows an allowing parent", allowed, &no, false, false},
		{"true under an allowing parent", allowed, &yes, true, false},
		{"true under a withholding parent", withheld, &yes, false, true},
	} {
		got, err := childSkipPermissions(c.caller, c.asked)
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
