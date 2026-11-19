package keyconfine

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain isolates this package's tests from any live tmux server: they
// start processes (see internal/tmuxtest).
func TestMain(m *testing.M) {
	tmuxtest.Main(m)
}
