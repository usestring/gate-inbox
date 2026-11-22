package app

import (
	"fmt"
	"os"

	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/launch"
)

// runPiPrelude is what a pi pane runs first (see launch's withPrelude): while
// it can still see the key dir, it writes the session's auto-mode token and
// its Claude Code MCP servers, as its flags ask, then execs the pi command in
// its place. The servers are read for the pane's own directory, which is the
// session's.
//
//	gate-inbox pi-prelude <session-id> [--auto-mode] [--claude-mcp] -- /bin/sh -c <command>
//
// The older pi-auto-mode-token verb is this with --auto-mode alone.
func runPiPrelude(verb string, args []string, configDir string) error {
	if len(args) < 3 {
		return fmt.Errorf("%s: want <session-id> [flags] -- <command>", verb)
	}
	id, autoMode, claudeMCP := args[0], verb == launch.AutoModeTokenVerb, false
	rest := args[1:]
	for len(rest) > 0 && rest[0] != "--" {
		switch rest[0] {
		case launch.PreludeAutoMode:
			autoMode = true
		case launch.PreludeClaudeMCP:
			claudeMCP = true
		default:
			return fmt.Errorf("%s: unknown flag %q", verb, rest[0])
		}
		rest = rest[1:]
	}
	if len(rest) < 2 {
		return fmt.Errorf("%s: want <session-id> [flags] -- <command>", verb)
	}
	if autoMode {
		launch.WriteAutoModeConfig(configDir, id)
	}
	if claudeMCP {
		home, _ := os.UserHomeDir()
		dir, _ := os.Getwd()
		launch.WriteClaudeMCP(configDir, id, home, dir)
	}
	// The pane started without the board's secrets, but this binary may have
	// filled in its own defaults for them on startup, and pi inherits
	// whatever this process holds.
	for _, name := range envname.BoardSecrets {
		os.Unsetenv(name)
	}
	return execTUI(rest[1:], nil)
}
