// Package extension is the public contract optional features are built
// against. An extension is compiled into the binary that runs the board: a
// module imports this package and the app package, lists its extensions in
// app.Options, and gets one executable whose TUI, CLI and per-session MCP
// server all see the same set.
//
// Registration is explicit and ordered. There is no init() registry: the
// set an executable carries is the slice its main passes, in that order, so
// the tool list a session sees does not depend on which files happened to be
// linked in.
//
// An extension is handed its own config section, a scoped view of the
// session it is registering into and a Host of narrow session services,
// never the operator's whole config, the board's model, its store or its
// tmux driver. Those are the host's, and
// freezing them into this contract would make every extension a co-owner of
// the event loop.
package extension

import (
	"fmt"
	"regexp"
)

// Descriptor names an extension.
//
// ID is the key its config lives under ([extensions.<id>]) and the name the
// board logs it by. It is lower case, starts with a letter, and may hold
// digits, '-' and '_'. It is part of the operator's config file, so changing
// it orphans every section written against the old one.
//
// Version is the extension's own version, recorded in logs so a report can
// say which build answered. It is informational; empty is allowed.
type Descriptor struct {
	ID      string
	Version string
}

// Extension is one optional feature.
//
// Configure is called once per process, before any capability is asked
// for, with this extension's own section of the operator's config. It
// decodes and validates that section and keeps what it needs. It must stay
// local: no network, no credentials, no subprocesses. It runs on the launch
// path of every session's MCP server, and a secret read here would be a
// secret read per session. A credential an extension can only fetch
// remotely is fetched when a tool that needs it is called.
//
// A section that is absent is still passed, as a Config whose Present
// reports false, so an extension decides for itself what "never
// configured" means.
type Extension interface {
	Descriptor() Descriptor
	Configure(Config) error
}

// Enabler is implemented by an extension its config can switch off. It is
// asked after Configure and before any capability; an extension that does
// not implement it is always on. It is asked on every session's launch
// path, so it is held to the same rule as Configure: local checks only.
type Enabler interface {
	Enabled() bool
}

// MCPProvider is implemented by an extension that adds tools to the MCP
// server every session is given.
//
// A tool that is never registered is a tool no agent can call and no agent
// pays context for, which is what "disabled" has to mean when the cost of a
// feature is measured in tokens per session.
type MCPProvider interface {
	RegisterMCP(r *Registrar, session SessionContext) error
}

// SessionContext is what an extension is told about the session whose MCP
// server it is registering into. It carries what an extension has actually
// needed so far and nothing else: a field added against a future tenant is
// a field every tenant has to read past. The session is identified by ID
// rather than by row, so an extension cannot reach into the board; Host is
// how it reads and acts on sessions instead, as this one.
type SessionContext struct {
	SessionID string
	// Host acts as SessionID. Nil where the host has no board to lend, as
	// in an extension's own unit tests.
	Host Host
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func validateDescriptor(d Descriptor) error {
	if !idPattern.MatchString(d.ID) {
		return fmt.Errorf("extension id %q must be lower case, start with a letter, and hold only letters, digits, '-' and '_'", d.ID)
	}
	return nil
}
