package launch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// A session's caller token never reaches its environment: the launch
// script that exports it is a file any sandboxed session can read, and
// with the token one session could act as another.
func TestALaunchCarriesNoCallerToken(t *testing.T) {
	dir := t.TempDir()
	tool := config.Tool{Command: "cat"}
	_, env, err := Environment(hooks.NewManager(dir), "plain", tool, tool.Command, "child01", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := parentseal.CallerToken(dir, "child01")
	for key, value := range env {
		if value == token {
			t.Fatalf("the launch env, which the pane's launch script exports, carries the caller token as %s", key)
		}
	}
}

// A claude launch is pointed at the shared server when the operator turned
// it on, and gets a server of its own otherwise, or when the server cannot
// be had.
func TestClaudeLaunchUsesTheSharedServer(t *testing.T) {
	tool := config.Tool{Command: "cat", MCP: "claude"}
	for name, c := range map[string]struct {
		config string
		url    string
		err    error
		shared bool
	}{
		"off by default":       {url: "http://127.0.0.1:4567"},
		"turned on":            {config: "[claude_code]\nshared_mcp = true\n", url: "http://127.0.0.1:4567", shared: true},
		"server not to be had": {config: "[claude_code]\nshared_mcp = true\n", err: errors.New("no server")},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(c.config), 0o600); err != nil {
				t.Fatal(err)
			}
			original := ensureShared
			ensureShared = func(string) (string, error) { return c.url, c.err }
			t.Cleanup(func() { ensureShared = original })
			command, _, err := Environment(hooks.NewManager(dir), "claude", tool, tool.Command, "abcd1234", "", "", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, after, _ := strings.Cut(command, "--mcp-config ")
			data, err := os.ReadFile(strings.Trim(strings.Fields(after)[0], "'"))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(data), `"type": "http"`); got != c.shared {
				t.Fatalf("shared server used %v, want %v:\n%s", got, c.shared, data)
			}
		})
	}
}
