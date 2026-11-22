package launch

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/hooks"
)

// A launch carries the operator's environment but not the board's own
// credentials, so its plan says what the pane it opens will get.
func TestALaunchLeavesTheBoardsSecretsBehind(t *testing.T) {
	for _, key := range envname.BoardSecrets {
		t.Setenv(key, "board-canary")
	}
	t.Setenv("GI_TEST_OPERATOR", "kept")
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat"}
	_, env, err := Compose(manager, "plain", tool, tool.Command, "abcd1234", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range envname.BoardSecrets {
		if _, set := env[key]; set {
			t.Fatalf("the launch carries %s", key)
		}
	}
	if env["GI_TEST_OPERATOR"] != "kept" {
		t.Fatalf("the operator's own variable was lost: %q", env["GI_TEST_OPERATOR"])
	}
}
