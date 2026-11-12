package app

import (
	"os"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain isolates this package's tests from any live tmux server, children
// included: see internal/tmuxtest. The shared fixture builds go with it.
func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Run(func() int {
		defer removeModuleBuilds()
		return m.Run()
	}))
}
