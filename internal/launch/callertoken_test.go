package launch

import (
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
