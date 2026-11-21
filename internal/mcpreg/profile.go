package mcpreg

import "encoding/json"

// ProfileFlag is the mcp verb's flag naming which of the server's tool
// profiles it serves; a server started without it serves every tool.
const ProfileFlag = "--profile"

// ProfileCore is the profile a spawned child's server runs: the delegation
// loop and nothing else. The tools it leaves out are for the sessions that
// run the board -- the operator's own, and the ones an extension launches
// for a role -- which keep the full server.
const ProfileCore = "core"

// serverArgs is the mcp verb's argv for profile, followed by extra.
func serverArgs(profile string, extra ...string) []string {
	args := []string{"mcp"}
	if profile != "" {
		args = append(args, ProfileFlag, profile)
	}
	return append(args, extra...)
}

// codexArgs is serverArgs as the TOML array a codex -c override takes. It
// names the codex steering so the server leaves out the delegation rule the
// launch's developer_instructions already carry.
func codexArgs(profile string) string {
	encoded, _ := json.Marshal(serverArgs(profile, SteeringFlag, "codex"))
	return string(encoded)
}
