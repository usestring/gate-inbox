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
			skip, children, err := SkipPermissions(c.source, name, tool, c.self, c.children)
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
			command, err := launch.WithSkipPermissions(name, tool, plan.Command, skip)
			if err != nil {
				t.Fatalf("%s %s: %v", name, c.what, err)
			}
			if strings.Contains(command, flag) != c.wantSkip {
				t.Errorf("%s %s: command = %q, want %s present = %v", name, c.what, command, flag, c.wantSkip)
			}
		}
	}
}

// opencode keeps a setting carried from the source and takes one the move
// asks for: its skip rides the generated config instead of a flag.
func TestMigrateOntoOpencodeKeepsSkipPermissions(t *testing.T) {
	cfg, err := config.LoadDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	tool := cfg.Tools["opencode"]
	skip, children, err := SkipPermissions(store.Session{SkipPermissions: true, ChildrenSkipPermissions: true}, "opencode", tool, nil, nil)
	if err != nil || !skip || !children {
		t.Fatalf("carried = %v %v %v, want both kept", skip, children, err)
	}
	on := true
	if skip, _, err := SkipPermissions(store.Session{}, "opencode", tool, &on, nil); err != nil || !skip {
		t.Fatalf("asked = %v %v, want skip on", skip, err)
	}
}

// A CLI with no way to skip drops a setting carried from the source, and
// refuses one the move asks for.
func TestMigrateOntoACLIWithNoWayToSkip(t *testing.T) {
	tool := config.Tool{Command: "plain"}
	skip, children, err := SkipPermissions(store.Session{SkipPermissions: true, ChildrenSkipPermissions: true}, "plain", tool, nil, nil)
	if err != nil || skip || !children {
		t.Fatalf("carried = %v %v %v, want the session's own dropped and its children's kept", skip, children, err)
	}
	on := true
	if _, _, err := SkipPermissions(store.Session{}, "plain", tool, &on, nil); err == nil {
		t.Fatal("a move onto a CLI with no way to skip asking for skip permissions was accepted")
	}
}
