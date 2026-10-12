package sessioncmd

import (
	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/store"
)

// mcpProfile is the MCP tool profile sess launches with. A child some
// session spawned and tracks gets the core tools only: it is handed one task
// and steered by its parent, and the board-level tools -- terminals, groups,
// reservations, accounts, an extension's run tools -- cost it context on
// every turn for calls it almost never makes. The operator's own sessions, a
// detached spawn the operator owns, and a session an extension launched for
// a role keep every tool.
func mcpProfile(sess store.Session) string {
	if sess.Role == "" && store.TrackerOf(sess) != "" {
		return mcpreg.ProfileCore
	}
	return ""
}
