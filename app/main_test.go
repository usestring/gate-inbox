package app

import (
	"os"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain drops the tmux server the test run was started from, so no tmux
// call this package makes, or any process it starts, can reach it.
func TestMain(m *testing.M) {
	tmuxtest.ClearInheritedTmuxEnv()
	os.Exit(m.Run())
}
