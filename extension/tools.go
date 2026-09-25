package extension

// ToolInfo is one CLI the operator's config.toml declares, as far as a
// launch needs to know it.
type ToolInfo struct {
	// Name is the key a SpawnRequest or LaunchRequest names it by.
	Name string
	// Shell says it opens a terminal rather than an agent: nothing can be
	// asked of it, and a launch of one is refused.
	Shell bool
	// TakesModel says a launch can put it on a chosen model; a Model on a
	// tool without it is refused.
	TakesModel bool
	// Models are the model names the config lists for it, which may lag the
	// CLI's own; empty when the config lists none.
	Models []string
	// HookStatus says the tool reports its status through hooks rather than
	// having it read off its pane.
	HookStatus bool
}
