package launch

import (
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
