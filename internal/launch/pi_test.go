package launch

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/piext"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// A pi-hooks launch loads the board's pi extension once with -e, from the
// stamped file the launch itself wrote, and gets a status file for it to
// report into. A dry run names the same file without writing it, and the
// launch carries none of Claude Code's wiring.
func TestEnvironmentLoadsThePiExtension(t *testing.T) {
	home := t.TempDir()
	manager := hooks.NewManager(home)
	pi := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi}
	path := piext.Path(manager.Dir())
	want := piext.Argv(tmux.ShellQuote(path))

	preview, _, err := Compose(manager, "pi", pi, pi.Command, "abcd1234", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Compose wrote the extension: %v", err)
	}
	command, env, err := Environment(manager, "pi", pi, pi.Command, "abcd1234", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{preview, command} {
		if strings.Count(got, " -e ") != 1 || !strings.HasSuffix(got, " "+want) {
			t.Fatalf("command = %q, want it to end in exactly one %q", got, want)
		}
		for _, claude := range []string{"--plugin-dir", "--settings", "--mcp-config"} {
			if strings.Contains(got, claude) {
				t.Fatalf("a pi launch carries Claude Code's %s: %q", claude, got)
			}
		}
	}
	if env[hooks.EnvStatusFile] != manager.StatusFile("abcd1234") {
		t.Fatalf("status file = %q, want %q", env[hooks.EnvStatusFile], manager.StatusFile("abcd1234"))
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the launch did not write the extension it loads: %v", err)
	}
	if !bytes.Equal(written, piext.Source()) {
		t.Fatal("the written extension is not this build's")
	}
}

// A launch clears what the last run of the same session left in its status
// file, so a pi row does not start out reading a turn that ended long ago.
func TestEnvironmentClearsAPiSessionsStaleStatus(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	if err := manager.Write("abcd1234", "finished"); err != nil {
		t.Fatal(err)
	}
	pi := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi}
	if _, _, err := Environment(manager, "pi", pi, pi.Command, "abcd1234", "", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.StatusFile("abcd1234")); !os.IsNotExist(err) {
		t.Fatalf("stale status file survived the launch: %v", err)
	}
}
