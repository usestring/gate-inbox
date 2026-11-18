package launch

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
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
	pi := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi, AutoMode: "off"}
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
	claude := config.Tool{Command: "cat"}
	if _, env, err = Compose(manager, "plain", claude, claude.Command, "abcd1234", "", "", false, nil); err != nil || env["TYPESAFE_API_KEY"] != "operator-key" {
		t.Fatalf("a non-pi launch lost the operator's key: %q, %v", env["TYPESAFE_API_KEY"], err)
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
	step := tmux.ShellQuote(Executable()) + " " + AutoModeTokenVerb + " " + tmux.ShellQuote("abcd1234") + " -- /bin/sh -c "
	inner, ok := strings.CutPrefix(command, step)
	if !ok || !strings.HasPrefix(inner, "'cat ") || !strings.Contains(inner, " -e ") {
		t.Fatalf("command = %q, want it to start with %q and run pi after", command, step)
	}
	if _, err := os.Stat(mcpshared.ClaudeConfigPath(manager.ConfigDir(), "abcd1234")); !os.IsNotExist(err) {
		t.Fatalf("composing the launch wrote the token itself: %v", err)
	}
	pi.AutoMode = "off"
	if command, _, err = Compose(manager, "pi", pi, pi.Command, "abcd1234", "", "", false, nil); err != nil || strings.Contains(command, AutoModeTokenVerb) {
		t.Fatalf("auto mode off: command = %q, %v", command, err)
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
