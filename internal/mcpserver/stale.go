package mcpserver

import (
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/internal/band"
	"github.com/usestring/gate-inbox/internal/managerbuild"
)

// staleNotice is prepended to list_sessions when this server is an older
// build of the manager than the board is running, and is empty otherwise.
//
// list_sessions is the place for it because the tool's own description
// tells an agent to call it first whenever the work involves another agent
// -- so the warning lands before the fan-out it is about, while the agent
// can still mention it, rather than after nine sessions have already been
// spawned into the wrong place.
//
// The agent cannot fix this itself; only the person can reconnect the
// server. So the notice asks for that, and says how far behind the server
// is, because a session twenty minutes behind and one six days behind
// deserve very different reactions.
func staleNotice(configDir string, now time.Time) string {
	behind, stale := managerbuild.StaleSince(configDir, now)
	if !stale {
		return ""
	}
	return band.Tag + " This session's Gate Inbox MCP server is an older build of the manager. " +
		"The board has been running a newer one for " + humanDuration(behind) +
		", so tools here can be missing arguments that exist now and a call may quietly do less than the current tool would. " +
		"Tell the user, and ask them to reconnect the gate-inbox MCP server (/mcp in Claude Code) or restart this session.\n\n"
}

// humanDuration rounds hard on purpose: the number is there to tell "just
// rebuilt" apart from "left behind a week ago", and a precise one invites
// the reader to reason about a clock that is only as good as the board's
// last start.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Hours()/24), "day")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
