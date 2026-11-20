// Package tooldrivers is how the core reaches the agent-CLI drivers a build's
// extensions supply. The composition root sets the resolver once; every
// place that switches on a tool's mcp or session_store style falls through to
// Lookup for a style it does not implement itself.
package tooldrivers

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/extension"
)

// Builtin are the styles the core implements itself. "none" registers no MCP
// server and captures nothing.
var Builtin = []string{"claude", "codex", "opencode", "none"}

// BuiltinSessionStores are the session_store styles the core reads beyond
// Builtin: "omp", whose session files package convo reads. They name no MCP
// style, so they are not Builtin.
var BuiltinSessionStores = []string{"omp"}

// Reserved are every style the core implements, which no extension's driver
// may claim.
func Reserved() []string { return append(slices.Clone(Builtin), BuiltinSessionStores...) }

// Timeout bounds one call into a driver: each is made while a launch, a poll
// pass or a fork waits on it.
const Timeout = 30 * time.Second

var (
	mu      sync.Mutex
	resolve = func() (map[string]extension.ToolDriver, error) { return nil, nil }
)

// Use sets how this process finds its drivers. It returns a func restoring
// the previous resolver, for tests.
func Use(resolver func() (map[string]extension.ToolDriver, error)) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	previous := resolve
	resolve = resolver
	return func() {
		mu.Lock()
		defer mu.Unlock()
		resolve = previous
	}
}

// Lookup is the driver for style, if an enabled extension supplies one. An
// error is the build's drivers failing to load, which is not the same as the
// style being unknown.
func Lookup(style string) (extension.ToolDriver, bool, error) {
	drivers, err := current()
	if err != nil {
		return nil, false, err
	}
	driver, ok := drivers[style]
	return driver, ok, nil
}

// Styles lists the styles the build's drivers answer to, sorted.
func Styles() []string {
	drivers, err := current()
	if err != nil {
		return nil
	}
	styles := make([]string, 0, len(drivers))
	for style := range drivers {
		styles = append(styles, style)
	}
	sort.Strings(styles)
	return styles
}

// Context is a context bounded by Timeout.
func Context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), Timeout)
}

func current() (map[string]extension.ToolDriver, error) {
	mu.Lock()
	r := resolve
	mu.Unlock()
	return r()
}
