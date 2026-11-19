package app

import (
	"fmt"
	"os"

	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/launch"
)

// runPiAutoModeToken is what a pi pane with auto mode on runs first (see
// launch's withAutoModeToken): it writes the session's auto-mode token while
// it can still see the key dir, then execs the pi command in its place.
//
//	gate-inbox pi-auto-mode-token <session-id> -- /bin/sh -c <command>
func runPiAutoModeToken(args []string, configDir string) error {
	if len(args) < 3 || args[1] != "--" {
		return fmt.Errorf("%s: want <session-id> -- <command>", launch.AutoModeTokenVerb)
	}
	launch.WriteAutoModeConfig(configDir, args[0])
	// The pane started without the board's secrets, but this binary may have
	// filled in its own defaults for them on startup, and pi inherits
	// whatever this process holds.
	for _, name := range envname.BoardSecrets {
		os.Unsetenv(name)
	}
	return execTUI(args[2:], nil)
}
