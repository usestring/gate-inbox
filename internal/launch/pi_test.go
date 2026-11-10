package launch

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/claudeplugin"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/mcpshared"
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

// A pi launch names the MCP config the extension registers the board's
// server from: the shared server's, which carries the session's caller token
// and lives in the key dir, when the operator turned it on and it answers;
// a config starting a server of the session's own otherwise. The token never
// rides the environment, only the file's path does.
func TestPiLaunchNamesTheBoardsMCPConfig(t *testing.T) {
	tool := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi}
	for name, c := range map[string]struct {
		config string
		err    error
		shared bool
	}{
		"off by default":       {},
		"turned on":            {config: "[claude_code]\nshared_mcp = true\n", shared: true},
		"server not to be had": {config: "[claude_code]\nshared_mcp = true\n", err: errors.New("no server")},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(c.config), 0o600); err != nil {
				t.Fatal(err)
			}
			original := ensureShared
			ensureShared = func(string) (string, error) { return "http://127.0.0.1:4567", c.err }
			t.Cleanup(func() { ensureShared = original })
			_, env, err := Environment(hooks.NewManager(dir), "pi", tool, tool.Command, "abcd1234", "", "", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			path := env[piext.MCPConfigEnv]
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s = %q: %v", piext.MCPConfigEnv, path, err)
			}
			var parsed struct {
				MCPServers map[string]struct {
					Type    string            `json:"type"`
					Command string            `json:"command"`
					Env     map[string]string `json:"env"`
					Headers map[string]string `json:"headers"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatal(err)
			}
			entry, ok := parsed.MCPServers[mcpreg.ServerName]
			if !ok {
				t.Fatalf("no %s server in %s", mcpreg.ServerName, data)
			}
			if got := entry.Type == "http"; got != c.shared {
				t.Fatalf("shared server used %v, want %v:\n%s", got, c.shared, data)
			}
			if c.shared && path != mcpshared.ClaudeConfigPath(dir, "abcd1234") {
				t.Fatalf("shared config at %s, want the key dir's %s", path, mcpshared.ClaudeConfigPath(dir, "abcd1234"))
			}
			if !c.shared && (entry.Command != Executable() || entry.Env[hooks.EnvSessionID] == "") {
				t.Fatalf("own server entry = %+v, want this binary told the session id", entry)
			}
			token := entry.Headers[mcpshared.CallerTokenHeader]
			if c.shared && token == "" {
				t.Fatalf("shared config carries no caller token:\n%s", data)
			}
			for key, value := range env {
				if token != "" && strings.Contains(value, token) {
					t.Fatalf("env %s carries the caller token", key)
				}
			}
		})
	}
}

// A pi launch names the board's plugin directory, written as a claude
// launch writes it, so the extension can offer its skills and commands.
func TestPiLaunchNamesTheBoardsPlugin(t *testing.T) {
	dir := t.TempDir()
	tool := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi}
	_, env, err := Environment(hooks.NewManager(dir), "pi", tool, tool.Command, "abcd1234", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := claudeplugin.Path(dir)
	if err != nil {
		t.Fatal(err)
	}
	if env[piext.PluginDirEnv] != want {
		t.Fatalf("%s = %q, want %q", piext.PluginDirEnv, env[piext.PluginDirEnv], want)
	}
	for _, rel := range []string{"skills/relay-child-dialog/SKILL.md", "commands/children.md", "commands/gi-sweep.md"} {
		if _, err := os.Stat(filepath.Join(want, rel)); err != nil {
			t.Fatalf("the launch did not write %s: %v", rel, err)
		}
	}
}
