package mcpreg

import (
	"path/filepath"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// PiSteering is what the board's pi extension adds to a managed pi session's
// system prompt: the naming ask when askRename, the delegation and parent
// rules every launch carries, and the turn-end report claude gets, under the
// same tool names (pi declares them as mcp__gate_inbox__*, and the board
// reads the report by its bare name). Pi shows a model only
// the first line of an MCP server's instructions, so none of this would
// reach it from the server; the extension injects it from
// before_agent_start.
func PiSteering(askRename bool) string {
	text := launchSteering("pi") + "\n" + reportStatusRule
	if askRename {
		text = string(renameSteering()) + "\n" + text
	}
	return text
}

// OwnServerConfig is the config that starts a Gate Inbox MCP server of the
// session's own, serving profile's tools, in the mcpServers shape Claude Code
// and pi both read: the server it names takes the session's id from
// GATE_INBOX_SESSION_ID. It is written content-stamped under hooksDir when
// write, and only named otherwise. A pi launch hands it to the board's pi
// extension when the shared server is off or unreachable.
func OwnServerConfig(exe, hooksDir, profile string, write bool) (string, error) {
	name, content := generatedPrefix+"mcp-claude.json", claudeConfig(exe, profile)
	if !write {
		return filepath.Join(hooksDir, hooks.GeneratedName(name, content)), nil
	}
	return writeConfig(hooksDir, name, content)
}
