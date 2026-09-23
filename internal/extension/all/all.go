// Package all assembles the extensions this build ships with.
//
// It is a package of its own so that the public extension package stays
// free of its tenants: the interface cannot import the things that
// implement it, and registering through init() would make the set depend on
// which files happened to be linked in. Adding an extension is one line
// here; a build outside this module lists its own in app.Options instead.
package all

import (
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/extension/artifacts"
)

// Extensions returns every extension, in the order their tools should be
// registered. Whether each one is switched on is its own config's question,
// not this list's. Each call returns fresh, unconfigured instances.
func Extensions() []extension.Extension {
	return []extension.Extension{
		artifacts.New(),
	}
}
