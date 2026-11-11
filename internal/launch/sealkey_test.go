package launch

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// A launch makes the board's seal key before the session starts, so a session
// that is shown only the key files existing at its start can check seals.
func TestLaunchMakesTheSealKeyFirst(t *testing.T) {
	dir := t.TempDir()
	tool := config.Tool{Command: "cat", StatusSource: hooks.StatusSourcePi}
	if _, _, err := Environment(hooks.NewManager(dir), "pi", tool, tool.Command, "abcd1234", "", "", false, nil); err != nil {
		t.Fatal(err)
	}
	if key, err := parentseal.PublicKey(dir); err != nil || key == nil {
		t.Fatalf("PublicKey after a launch = %x, %v; want the board's key", key, err)
	}
}
