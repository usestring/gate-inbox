package mcpserver

import "github.com/usestring/gate-inbox/internal/mcpreg"

// PiInstructions is the steering the board's pi extension injects into a
// managed pi session's system prompt: this server's instructions and the
// launch steering (mcpreg.PiSteering). Pi declares the server's tools to the
// model but shows it only the first line of the server's instructions, so
// the whole block has to arrive the way a launch's own steering does.
func PiInstructions(configDir string) string {
	return serverInstructions + "\n\n" + mcpreg.PiSteering(namingAsks(configDir))
}
