package cli

import (
	"os"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain isolates this package's tests from any live tmux server, children
// included: see internal/tmuxtest. Run by a stand-in executable with
// hookBinEnv set, it is the installed binary's hook verb instead; see
// installStandIn.
func TestMain(m *testing.M) {
	if os.Getenv(hookBinEnv) == "1" {
		os.Exit(runStandIn())
	}
	tmuxtest.Main(m)
}
