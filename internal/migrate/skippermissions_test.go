package migrate

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/store"
)

// A move keeps the source's settings unless it names its own, and the moved
// session's command line carries the flag exactly when it ends up on: turned
// on by the move, or turned off by it, for claude and codex alike.
func TestMigrateTurnsSkipPermissionsOnAndOff(t *testing.T) {
	cfg, err := config.LoadDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	on, off := true, false
	for _, name := range []string{"claude", "codex"} {
		tool := cfg.Tools[name]
		flag := tool.SkipPermissionsFlag
		for _, c := range []struct {
			what           string
			source         store.Session
			self, children *bool
			wantSkip       bool
			wantChildren   bool
		}{
			{"kept off", store.Session{}, nil, nil, false, false},
			{"kept on", store.Session{SkipPermissions: true, ChildrenSkipPermissions: true}, nil, nil, true, true},
			{"turned on", store.Session{}, &on, &on, true, true},
			{"turned off", store.Session{SkipPermissions: true, ChildrenSkipPermissions: true}, &off, &off, false, false},
		} {
			skip, children, err := SkipPermissions(c.source, tool, c.self, c.children)
			if err != nil {
				t.Fatalf("%s %s: %v", name, c.what, err)
			}
			if skip != c.wantSkip || children != c.wantChildren {
				t.Errorf("%s %s: skip %v children %v, want %v %v", name, c.what, skip, children, c.wantSkip, c.wantChildren)
			}
			plan, err := launch.Assemble(name, tool, "read the transcript", "", false, "", "")
			if err != nil {
				t.Fatalf("%s Assemble: %v", name, err)
			}
			command, err := launch.WithSkipPermissions(tool, plan.Command, skip)
			if err != nil {
				t.Fatalf("%s %s: %v", name, c.what, err)
			}
			if strings.Contains(command, flag) != c.wantSkip {
				t.Errorf("%s %s: command = %q, want %s present = %v", name, c.what, command, flag, c.wantSkip)
			}
		}
	}
}

// A CLI with no flag drops a setting carried from the source, and refuses
// one the move asks for.
func TestMigrateOntoACLIWithoutTheFlag(t *testing.T) {
	cfg, err := config.LoadDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	tool := cfg.Tools["opencode"]
	skip, children, err := SkipPermissions(store.Session{SkipPermissions: true, ChildrenSkipPermissions: true}, tool, nil, nil)
	if err != nil || skip || !children {
		t.Fatalf("carried = %v %v %v, want the session's own dropped and its children's kept", skip, children, err)
	}
	on := true
	if _, _, err := SkipPermissions(store.Session{}, tool, &on, nil); err == nil {
		t.Fatal("a move onto opencode asking for skip permissions was accepted")
	}
}
