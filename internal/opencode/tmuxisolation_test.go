package opencode

import (
	"os"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain isolates this package's tests from any live tmux server, children
// included: see internal/tmuxtest. Run as "opencode serve" with the fake's
// variable set, the test binary is the stand-in server shared_test.go starts.
func TestMain(m *testing.M) {
	if os.Getenv(fakeServerEnv) != "" {
		fakeServe()
		return
	}
	tmuxtest.Main(m)
}
