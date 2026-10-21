package codexprobe

import (
	"os"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain isolates this package's tests from any live tmux server, children
// included: see internal/tmuxtest. The probe's codex re-executes this binary
// as its MCP server and hook command; those runs are fixtures, not tests.
func TestMain(m *testing.M) {
	if role := os.Getenv(roleEnv); role != "" {
		os.Exit(runFixture(role))
	}
	tmuxtest.Main(m)
}
