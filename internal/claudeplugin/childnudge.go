package claudeplugin

import "github.com/usestring/gate-inbox/internal/hooks"

// A parent hears about a child's dialog from its own hooks: at the start of
// a turn, when a turn would end with one still standing, and in the status
// line, which PostToolUse keeps current between the two.
func init() {
	register(
		Hook{Event: "UserPromptSubmit", Command: hooks.ChildDialogsCommand()},
		Hook{Event: "Stop", Command: hooks.ChildDialogsCommand()},
		Hook{Event: "PostToolUse", Matcher: "*", Command: hooks.ThrottledChildDialogsCommand()},
	)
}
