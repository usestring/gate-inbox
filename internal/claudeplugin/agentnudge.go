package claudeplugin

import "github.com/usestring/gate-inbox/internal/envname"

// agentNudgeCommand runs the agent-nudge hook (see package agentnudge) only
// in a session the board launched: one with a status file, the manager's
// path and a session id. Anywhere else it prints nothing and the call runs.
const agentNudgeCommand = `f="$` + envname.StatusFile + `"; [ -z "$f" ] || { [ -z "$` + envname.Executable +
	`" ] || [ -z "$` + envname.SessionID + `" ] || "$` + envname.Executable + `" hook agent-nudge 2>/dev/null; }; exit 0`

func init() {
	register(Hook{Event: "PreToolUse", Matcher: "Agent|Task", Command: agentNudgeCommand})
}
