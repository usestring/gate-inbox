package launch

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/claudeplugin"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/mcpshared"
	"github.com/usestring/gate-inbox/internal/parentseal"
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
	pi := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi, AutoMode: "off", MCPServers: "off"}
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

// An omp launch runs the same extension, and also loads a plugin of its own
// holding the board's MCP server, since omp has no registerMcpServer. A dry
// run names that plugin without writing it.
func TestEnvironmentGivesOmpTheBoardsServerThroughAPlugin(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	omp := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi, AutoMode: "off", MCPServers: "off"}
	preview, _, err := Compose(manager, "omp", omp, omp.Command, "abcd1234", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	command, env, err := Environment(manager, "omp", omp, omp.Command, "abcd1234", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	plugin := piext.OmpPluginPath(env[piext.MCPConfigEnv], "abcd1234")
	want := piext.Argv(tmux.ShellQuote(piext.Path(manager.Dir()))) + " " + piext.OmpPluginArgv(tmux.ShellQuote(plugin))
	for _, got := range []string{preview, command} {
		if !strings.HasSuffix(got, " "+want) {
			t.Fatalf("command = %q, want it to end in %q", got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(plugin, ".mcp.json")); err != nil {
		t.Fatalf("the launch did not write the plugin it loads: %v", err)
	}
	pi := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi, AutoMode: "off"}
	if command, _, err := Compose(manager, "pi", pi, pi.Command, "abcd1234", "", "", false, nil); err != nil || strings.Contains(command, "--plugin-dir") {
		t.Fatalf("a pi launch loads omp's plugin: %q, %v", command, err)
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

// The board's TypeSafe key is withheld from a pi pane, so neither the launch
// script nor the environment the tmux server would pass on carries it.
func TestAPiLaunchWithholdsTheTypeSafeKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "operator-key")
	manager := hooks.NewManager(t.TempDir())
	pi := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi}
	_, env, err := Compose(manager, "pi", pi, pi.Command, "abcd1234", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if value := env["TYPESAFE_API_KEY"]; value != tmux.Withheld {
		t.Fatalf("TYPESAFE_API_KEY = %q, want it withheld", value)
	}
}

// A pi pane with auto mode on writes its own token before pi starts: the
// process composing the launch may be a confined session's MCP server, which
// cannot see the key dir, while the pane is the tmux server's child. With
// auto mode off the pane runs pi directly.
func TestAPiLaunchWithAutoModeWritesItsTokenFromThePane(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	pi := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi}
	command, _, err := Compose(manager, "pi", pi, pi.Command, "abcd1234", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	step := tmux.ShellQuote(Executable()) + " " + PreludeVerb + " " + tmux.ShellQuote("abcd1234") + " " + PreludeAutoMode + " " + PreludeClaudeMCP + " -- /bin/sh -c "
	inner, ok := strings.CutPrefix(command, step)
	if !ok || !strings.HasPrefix(inner, "'cat ") || !strings.Contains(inner, " -e ") {
		t.Fatalf("command = %q, want it to start with %q and run pi after", command, step)
	}
	if _, err := os.Stat(mcpshared.ClaudeConfigPath(manager.ConfigDir(), "abcd1234")); !os.IsNotExist(err) {
		t.Fatalf("composing the launch wrote the token itself: %v", err)
	}
	pi.AutoMode = "off"
	if command, _, err = Compose(manager, "pi", pi, pi.Command, "abcd1234", "", "", false, nil); err != nil || strings.Contains(command, PreludeAutoMode) {
		t.Fatalf("auto mode off: command = %q, %v", command, err)
	}
	pi.MCPServers = "off"
	if command, _, err = Compose(manager, "pi", pi, pi.Command, "abcd1234", "", "", false, nil); err != nil || strings.Contains(command, PreludeVerb) {
		t.Fatalf("auto mode and Claude MCP servers off: command = %q, %v", command, err)
	}
}

// A pi launch names the key-dir file its extension registers the Claude Code
// MCP servers from; an omp launch also loads that directory as a plugin.
// mcp_servers = "off" gives neither. Only paths reach the command and the
// environment.
func TestAPiLaunchGetsTheClaudeMCPServersFromTheKeyDir(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	keyDir := parentseal.KeyDir(manager.ConfigDir())
	for _, c := range []struct {
		name string
		tool config.Tool
		omp  bool
	}{
		{"pi", config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi}, false},
		{"omp", config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi, SessionStore: "omp"}, true},
	} {
		command, env, err := Compose(manager, c.name, c.tool, c.tool.Command, "abcd1234", "", "", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := env[piext.ClaudeMCPEnv], piext.ClaudeMCPConfig(keyDir, "abcd1234"); got != want {
			t.Errorf("%s: %s = %q, want %q", c.name, piext.ClaudeMCPEnv, got, want)
		}
		if got := strings.Contains(command, "--plugin-dir "+tmux.ShellQuote(piext.ClaudeMCPDir(keyDir, "abcd1234"))) || strings.Contains(command, piext.ClaudeMCPDir(keyDir, "abcd1234")); got != c.omp {
			t.Errorf("%s: command %q loads the Claude MCP plugin = %v, want %v", c.name, command, got, c.omp)
		}
		c.tool.MCPServers = "off"
		command, env, err = Compose(manager, c.name, c.tool, c.tool.Command, "abcd1234", "", "", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := env[piext.ClaudeMCPEnv]; ok || strings.Contains(command, "claude-mcp-") {
			t.Errorf("%s with mcp_servers off: command %q, env %v", c.name, command, env[piext.ClaudeMCPEnv])
		}
	}
}

// The token is written only for a well-formed id, and only when the board's
// own shared server answers; an existing config is kept.
func TestWriteAutoModeConfigNeedsTheBoardsServer(t *testing.T) {
	dir := t.TempDir()
	path := mcpshared.ClaudeConfigPath(dir, "abcd1234")
	WriteAutoModeConfig(dir, "abcd1234")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("wrote a token with no server running: %v", err)
	}
	listener, lock, err := mcpshared.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proof, _ := parentseal.ServerProof(dir, r.URL.Query().Get("nonce"))
		_, _ = w.Write([]byte(proof))
	})}
	go server.Serve(listener)
	defer server.Close()
	WriteAutoModeConfig(dir, "../escape")
	WriteAutoModeConfig(dir, "abcd1234")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no token with the board's server up: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*escape*")); len(matches) > 0 {
		t.Fatalf("a malformed id got a token: %v", matches)
	}
}
