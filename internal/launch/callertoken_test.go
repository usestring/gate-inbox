package launch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// A launch hands the session its own caller token, never the spawning
// session's; a plan composed without side effects mints no key for it.
func TestLaunchHandsTheSessionItsOwnCallerToken(t *testing.T) {
	dir := t.TempDir()
	manager := hooks.NewManager(dir)
	tool := config.Tool{Command: "cat"}
	t.Setenv(envname.SessionToken, "the-parents-token")

	_, env, err := Compose(manager, "plain", tool, tool.Command, "child01", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token, set := env[envname.SessionToken]; set {
		t.Fatalf("a plan composed before any launch carries token %q", token)
	}

	_, env, err = Environment(manager, "plain", tool, tool.Command, "child01", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !parentseal.VerifyCaller(dir, "child01", env[envname.SessionToken]) {
		t.Fatalf("launch env token %q does not prove child01", env[envname.SessionToken])
	}

	_, planned, err := Compose(manager, "plain", tool, tool.Command, "child01", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if planned[envname.SessionToken] != env[envname.SessionToken] {
		t.Fatal("a plan composed once the key exists differs from the launch")
	}
}

func TestAnExtensionCannotSetTheCallerToken(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat"}
	_, _, err := Compose(manager, "plain", tool, tool.Command, "child01", "", "", false, map[string]string{envname.SessionToken: "forged"})
	if err == nil {
		t.Fatal("an extension set the caller token")
	}
}

// A claude launch is pointed at the shared server, unless the operator turned
// it off or the server cannot be had, when it gets a server of its own.
func TestClaudeLaunchUsesTheSharedServer(t *testing.T) {
	tool := config.Tool{Command: "cat", MCP: "claude"}
	for name, c := range map[string]struct {
		config string
		url    string
		err    error
		shared bool
	}{
		"on by default":        {url: "http://127.0.0.1:4567", shared: true},
		"turned off":           {config: "[claude_code]\nshared_mcp = false\n", url: "http://127.0.0.1:4567"},
		"server not to be had": {err: errors.New("no server")},
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
