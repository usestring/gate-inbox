package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
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
	if got := run("status"); !strings.Contains(got, "not registered") || !strings.Contains(got, "will not register") {
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
// user's settings alone.
func TestBoardStartupSkipsGlobalHooksForAScratchHome(t *testing.T) {
	claudeDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	home := t.TempDir()
	if !underTempDir(home) {
		t.Fatalf("%s is not recognised as scratch", home)
	}
	registerGlobalHooks(home)
	if _, err := os.Stat(filepath.Join(claudeDir, "settings.json")); err == nil {
		t.Fatal("a scratch board wrote the user's settings")
	}
	if underTempDir("/var/lib/gate-inbox") {
		t.Fatal("a real home was taken for scratch")
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
			path := filepath.Join(t.TempDir(), "settings.json")
			setup(path)
			before, _ := os.ReadFile(path)
			registerGlobalHooksAt(path, t.TempDir(), "/bin/gate-inbox")
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
