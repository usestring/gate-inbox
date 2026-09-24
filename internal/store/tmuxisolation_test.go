package store

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain isolates this package's tests from any live tmux server, children
// included: see internal/tmuxtest.
func TestMain(m *testing.M) {
	tmuxtest.Main(m)
}
