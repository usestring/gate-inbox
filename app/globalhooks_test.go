package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/mcprelay"
)

// claude-hooks install, status and uninstall against a scratch user settings
// file, never the real one: CLAUDE_CONFIG_DIR points Claude Code, and this
// command, somewhere else.
func TestClaudeHooksCommandInstallsAndRemoves(t *testing.T) {
	claudeDir, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("GATE_INBOX_HOME", home)
	calls := fakeClaude(t, filepath.Join(claudeDir, ".claude.json"))
	settings := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := runClaudeHooks(&out, args, home); err != nil {
			t.Fatalf("claude-hooks %v: %v", args, err)
		}
		return out.String()
	}

	if got := run("status"); !strings.Contains(got, "not registered") {
		t.Fatalf("status before install = %q", got)
	}
	if got := run("install"); !strings.Contains(got, "registered Gate Inbox's hooks in "+settings) {
		t.Fatalf("install = %q", got)
	}
	if got := run("install"); !strings.Contains(got, "already carries") {
		t.Fatalf("second install = %q", got)
	}
	if got := run(); !strings.Contains(got, ": registered\n") || !strings.HasSuffix(got, ": MCP relay registered\n") {
		t.Fatalf("status after install = %q", got)
	}
	if got := readCalls(t, calls); got != "mcp add-json -s user gate-inbox" {
		t.Fatalf("claude calls after two installs = %q, want one add", got)
	}
	raw, _ := os.ReadFile(settings)
	if !strings.HasPrefix(string(raw), `{"theme":"dark","hooks":`) || !strings.Contains(string(raw), "gate-inbox-global-hook") {
		t.Fatalf("settings after install:\n%s", raw)
	}

	if got := run("uninstall"); !strings.Contains(got, "removed Gate Inbox's hooks") || !strings.Contains(got, "removed the Gate Inbox MCP relay") {
		t.Fatalf("uninstall = %q", got)
	}
	if !hooks.NewManager(home).GlobalDisabled() {
		t.Fatal("uninstall did not keep the board from registering again")
	}
	if got := run("status"); !strings.Contains(got, "not registered") || !strings.Contains(got, "switched off in Settings; the board removes them") {
		t.Fatalf("status after uninstall = %q", got)
	}
	raw, _ = os.ReadFile(settings)
	if string(raw) != `{"theme":"dark"}` {
		t.Fatalf("settings after uninstall:\n%s", raw)
	}
	run("install")
	if hooks.NewManager(home).GlobalDisabled() {
		t.Fatal("install left the board's registration switched off")
	}

	var out bytes.Buffer
	if err := runClaudeHooks(&out, []string{"bogus"}, home); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("an unknown verb: %v", err)
	}
}

// A board on a scratch home -- every test's, and any trial run's -- leaves the
// user's settings alone, and starts no loop that could touch them later.
func TestBoardStartupSkipsGlobalHooksForAScratchHome(t *testing.T) {
	claudeDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	calls := fakeClaude(t, filepath.Join(claudeDir, ".claude.json"))
	home := t.TempDir()
	if !underTempDir(home) {
		t.Fatalf("%s is not recognised as scratch", home)
	}
	keepClaudeSetup(home, true)()
	if _, err := os.Stat(filepath.Join(claudeDir, "settings.json")); err == nil {
		t.Fatal("a scratch board wrote the user's settings")
	}
	if got := readCalls(t, calls); got != "" {
		t.Fatalf("a scratch board ran claude: %q", got)
	}
	if underTempDir("/var/lib/gate-inbox") {
		t.Fatal("a real home was taken for scratch")
	}
}

// scratchSetup is a claudeSetup over a scratch Claude Code config and a fake
// claude, called directly because keepClaudeSetup rightly refuses the scratch
// home a test has.
func scratchSetup(t *testing.T, configOn bool) (setup *claudeSetup, settings, state, calls string) {
	t.Helper()
	claudeDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	state = filepath.Join(claudeDir, ".claude.json")
	calls = fakeClaude(t, state)
	setup = newClaudeSetup(t.TempDir(), configOn)
	setup.bin = func() string { return "/opt/gate-inbox/bin/gate-inbox" }
	return setup, filepath.Join(claudeDir, "settings.json"), state, calls
}

func setupInPlace(t *testing.T, setup *claudeSetup, settings, state string) bool {
	t.Helper()
	hooksOn, err := hooks.GlobalRegistered(settings, setup.dir, setup.bin())
	if err != nil {
		t.Fatal(err)
	}
	relay, err := mcprelay.Lookup(state, setup.dir, setup.bin())
	if err != nil {
		t.Fatal(err)
	}
	return hooksOn && relay == mcprelay.Current
}

// With nothing set up, one pass registers both; a second finds them in
// place and neither writes nor runs claude.
func TestClaudeSetupRegistersWithNoCommand(t *testing.T) {
	setup, settings, state, calls := scratchSetup(t, true)
	setup.sync()
	if !setupInPlace(t, setup, settings, state) {
		t.Fatal("the first pass left the setup incomplete")
	}
	info, _ := os.Stat(settings)
	setup.sync()
	if again, _ := os.Stat(settings); !os.SameFile(info, again) {
		t.Fatal("a pass with nothing to change rewrote the settings")
	}
	if got := readCalls(t, calls); got != "mcp add-json -s user gate-inbox" {
		t.Fatalf("claude calls = %q, want one add", got)
	}
}

// Entries deleted by hand while the board runs come back on the loop's next
// pass, with no restart.
func TestClaudeSetupLoopRestoresDeletedEntries(t *testing.T) {
	setup, settings, state, _ := scratchSetup(t, true)
	setup.sync()
	if err := os.WriteFile(settings, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		setup.run(ctx, 10*time.Millisecond, time.Hour)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !setupInPlace(t, setup, settings, state) {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("the loop did not put the deleted entries back")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if raw, _ := os.ReadFile(settings); !strings.HasPrefix(string(raw), `{"theme":"dark","hooks":`) {
		t.Fatalf("settings after the repair:\n%s", raw)
	}
}

// A binary that moved -- a new home, an upgrade installed elsewhere -- has
// both entries rewritten to name the new path.
func TestClaudeSetupFollowsAMovedBinary(t *testing.T) {
	setup, settings, state, calls := scratchSetup(t, true)
	setup.sync()
	setup.bin = func() string { return "/usr/local/bin/gate-inbox" }
	setup.sync()
	if !setupInPlace(t, setup, settings, state) {
		t.Fatal("the entries still name the old binary")
	}
	if raw, _ := os.ReadFile(settings); strings.Contains(string(raw), "/opt/gate-inbox") {
		t.Fatalf("the old binary is still in the settings:\n%s", raw)
	}
	want := "mcp add-json -s user gate-inbox\nmcp remove -s user gate-inbox\nmcp add-json -s user gate-inbox"
	if got := readCalls(t, calls); got != want {
		t.Fatalf("claude calls = %q, want %q", got, want)
	}
}

// Opting out takes the entries out rather than only ceasing to add them,
// from config.toml at startup or from the Settings switch while running;
// switching it back on restores them.
func TestClaudeSetupOptOutRemovesEntries(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		on, settings, state, _ := scratchSetup(t, true)
		on.sync()
		off := newClaudeSetup(on.dir, false)
		off.bin = on.bin
		off.sync()
		if raw, _ := os.ReadFile(settings); strings.Contains(string(raw), "gate-inbox-global-hook") {
			t.Fatalf("config opt-out left the hooks:\n%s", raw)
		}
		if relay, _ := mcprelay.Lookup(state, on.dir, on.bin()); relay != mcprelay.Absent {
			t.Fatalf("config opt-out left the relay: %v", relay)
		}
	})
	t.Run("settings switch", func(t *testing.T) {
		setup, settings, state, _ := scratchSetup(t, true)
		setup.sync()
		manager := hooks.NewManager(setup.dir)
		if err := manager.SetGlobalDisabled(true); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			setup.run(ctx, time.Hour, 10*time.Millisecond)
		}()
		defer func() { cancel(); <-done }()
		waitFor(t, "the switch to remove the entries", func() bool {
			relay, _ := mcprelay.Lookup(state, setup.dir, setup.bin())
			raw, _ := os.ReadFile(settings)
			return relay == mcprelay.Absent && !strings.Contains(string(raw), "gate-inbox-global-hook")
		})
		if err := manager.SetGlobalDisabled(false); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the switch to restore the entries", func() bool {
			return setupInPlace(t, setup, settings, state)
		})
	})
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A settings file the board cannot use costs a warning, never the startup,
// and is left exactly as it was.
func TestBoardStartupLeavesAnUnusableSettingsFileAlone(t *testing.T) {
	for name, setup := range map[string]func(path string){
		"malformed": func(path string) { os.WriteFile(path, []byte(`{"hooks": [`), 0o644) },
		"read-only": func(path string) {
			os.WriteFile(path, []byte(`{"theme": "dark"}`), 0o644)
			os.Chmod(path, 0o444)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "read-only" && os.Geteuid() == 0 {
				t.Skip("root writes through any mode")
			}
			claude, _, _, _ := scratchSetup(t, true)
			path, _ := hooks.GlobalSettingsPath()
			setup(path)
			before, _ := os.ReadFile(path)
			claude.sync()
			claude.sync()
			if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
				t.Fatalf("the settings file changed:\n%s", after)
			}
		})
	}
}

// fakeClaude puts a claude on PATH that records each call's first five
// arguments and keeps the user-scope MCP entry in the state file at state the
// way the real one does, for the two commands the relay registration runs.
func fakeClaude(t *testing.T, state string) string {
	t.Helper()
	bin, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	script := `#!/bin/sh
printf '%s %s %s %s %s\n' "$1" "$2" "$3" "$4" "$5" >> ` + calls + `
case "$2" in
add-json) printf '{"mcpServers":{"%s":%s}}' "$5" "$6" > ` + state + `;;
remove) printf '{}' > ` + state + `;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func readCalls(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}
