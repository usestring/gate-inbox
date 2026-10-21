package hooks

// HookMatcher and HookCommand are one hooks.json entry, in the shape both a
// settings file and a plugin's hooks/hooks.json take.
type (
	HookMatcher = hookMatcher
	HookCommand = hookCommand
)

// LaunchHooks are the hooks every claude session the board launches carries,
// by event, for the Gate Inbox plugin to write into its hooks/hooks.json.
func LaunchHooks() map[string][]HookMatcher {
	return launchHooks()
}
