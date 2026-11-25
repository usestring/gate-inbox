package accounts

import (
	"os"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain isolates this package's tests from any live tmux server, children
// included: see internal/tmuxtest. It also keeps the shared account listing
// in a directory of its own, so a test that lists never reads or overwrites
// the operator's.
func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Run(func() int {
		if dir, err := os.MkdirTemp("", "gate-inbox-accounts-listings-"); err == nil {
			UseListingDir(dir)
			defer os.RemoveAll(dir)
		}
		return m.Run()
	}))
}
