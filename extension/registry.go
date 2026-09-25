package extension

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Registry is an ordered, validated set of extensions. The host builds one
// per process from the slice app.Options carries; extensions never see it.
type Registry struct {
	extensions []Extension
	ids        []string
	configured bool
	// disabled holds the extensions Configure switched off, and why.
	disabled map[string]error
}

// NewRegistry validates the set: every extension has a well-formed ID and no
// two share one. Order is kept as given, and it is the order their tools
// are registered in.
func NewRegistry(extensions []Extension) (*Registry, error) {
	registry := &Registry{}
	var errs []error
	for i, ext := range extensions {
		if ext == nil {
			errs = append(errs, fmt.Errorf("extension %d is nil", i))
			continue
		}
		desc := ext.Descriptor()
		if err := validateDescriptor(desc); err != nil {
			errs = append(errs, err)
			continue
		}
		if slices.Contains(registry.ids, desc.ID) {
			errs = append(errs, fmt.Errorf("extension id %q is registered twice", desc.ID))
			continue
		}
		registry.extensions = append(registry.extensions, ext)
		registry.ids = append(registry.ids, desc.ID)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return registry, nil
}

// IDs lists the registered extensions in registration order.
func (r *Registry) IDs() []string {
	return slices.Clone(r.ids)
}

// Configure hands each extension its own section of the [extensions] table,
// and its own data directory under configDir.
//
// A section an extension refuses disables that extension alone: it
// registers no tools, supplies no account pool, starts nothing on the
// board, and its launch hooks are skipped; the report names it with the
// reason, while every other extension carries on. The one hook that is not
// skipped is a SpawnPolicy, which fails closed: see SessionHooks. A section no
// registered extension owns disables nothing -- there is nothing to
// disable -- and is reported as a warning: it is either a typo or a config
// written for a different build, and neither should pass silently.
//
// Every extension is configured even when an earlier one fails, so one run
// reports every problem. Calling Configure again starts the report afresh.
func (r *Registry) Configure(configDir string, sections map[string]map[string]any) ConfigReport {
	var report ConfigReport
	for id := range sections {
		if !slices.Contains(r.ids, id) {
			report.Unknown = append(report.Unknown, id)
		}
	}
	slices.Sort(report.Unknown)
	report.known = slices.Clone(r.ids)
	r.disabled = map[string]error{}
	for i, ext := range r.extensions {
		if err := configureOne(ext, configDir, r.ids[i], sections); err != nil {
			r.disabled[r.ids[i]] = err
			report.Disabled = append(report.Disabled, DisabledExtension{ID: r.ids[i], Err: err})
		}
	}
	r.configured = true
	return report
}

// configureOne turns a panic into an error, for the same reason
// registerOne does: one extension's bug must not take the others down.
func configureOne(ext Extension, configDir, id string, sections map[string]map[string]any) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panicked while configuring: %v", recovered)
		}
	}()
	cfg := NewConfig(sections[id])
	if configDir != "" {
		cfg = cfg.WithDataDir(DataDir(configDir, id))
	}
	return ext.Configure(cfg)
}

// Configured reports whether Configure has run.
func (r *Registry) Configured() bool {
	return r.configured
}

// Disabled is why the extension with id was switched off by its config, or
// nil when it was not. A command that needs that extension fails with
// this rather than with a vaguer "not available".
func (r *Registry) Disabled(id string) error {
	if err, ok := r.disabled[id]; ok {
		return disabledError(id, err)
	}
	return nil
}

func disabledError(id string, err error) error {
	return fmt.Errorf("extension %q is disabled: [extensions.%s]: %w", id, id, err)
}

// DisabledExtension is one extension its config section switched off.
type DisabledExtension struct {
	ID  string
	Err error
}

// ConfigReport is what Configure made of the [extensions] table.
type ConfigReport struct {
	// Disabled are the extensions whose section was refused, in
	// registration order.
	Disabled []DisabledExtension
	// Unknown are sections no extension in this build owns, sorted.
	Unknown []string

	known []string
}

// OK reports whether every section was accepted and owned.
func (c ConfigReport) OK() bool {
	return len(c.Disabled) == 0 && len(c.Unknown) == 0
}

// Notes is the report as short lines for an operator or an agent to read:
// one per disabled extension, then one for the unowned sections.
func (c ConfigReport) Notes() []string {
	var notes []string
	for _, d := range c.Disabled {
		notes = append(notes, fmt.Sprintf("%s disabled: [extensions.%s]: %v", d.ID, d.ID, d.Err))
	}
	if len(c.Unknown) > 0 {
		sections := make([]string, len(c.Unknown))
		for i, id := range c.Unknown {
			sections[i] = "[extensions." + id + "]"
		}
		notes = append(notes, fmt.Sprintf("ignored %s: no extension in this build owns it (this build has: %s)",
			strings.Join(sections, ", "), orNone(c.known)))
	}
	return notes
}

// AccountPool is the pool the enabled AccountPoolProvider supplies, or nil
// when no extension in this build supplies one. On a registry nothing has
// configured yet, only the providers are configured, from sections and under
// configDir: a CLI command routing one launch has no use for the rest.
// Two enabled providers are refused rather than one silently winning. When
// no provider supplies a pool because its config disabled it, the error
// says so, so a routed launch fails with the reason rather than as though
// the build had no pool at all.
func (r *Registry) AccountPool(configDir string, sections map[string]map[string]any) (AccountPool, error) {
	found := r.providers(func(ext Extension) bool {
		_, ok := ext.(AccountPoolProvider)
		return ok
	})
	disabled := r.configureOnly(found, configDir, sections)
	var pool AccountPool
	var owners []string
	var refused []error
	for _, i := range found {
		if err, off := disabled[r.ids[i]]; off {
			refused = append(refused, disabledError(r.ids[i], err))
			continue
		}
		if !enabled(r.extensions[i]) {
			continue
		}
		pool = r.extensions[i].(AccountPoolProvider).AccountPool()
		if v := reflect.ValueOf(pool); pool == nil || (v.Kind() == reflect.Pointer && v.IsNil()) {
			return nil, fmt.Errorf("extension %q is enabled but supplied no account pool", r.ids[i])
		}
		owners = append(owners, r.ids[i])
	}
	if len(owners) > 1 {
		return nil, fmt.Errorf("more than one extension supplies an account pool: %s", strings.Join(owners, ", "))
	}
	if len(owners) == 0 && len(refused) > 0 {
		return nil, errors.Join(refused...)
	}
	return pool, nil
}

// ToolDrivers are the drivers every enabled ToolDriverProvider supplies,
// keyed by style. As with AccountPool, a registry nothing has configured yet
// configures only the providers, and a provider its section disabled
// supplies none. reserved names the styles the host implements itself; a
// driver can take none of them, nor a style another driver took first.
func (r *Registry) ToolDrivers(configDir string, sections map[string]map[string]any, reserved []string) (map[string]ToolDriver, error) {
	found := r.providers(func(ext Extension) bool {
		_, ok := ext.(ToolDriverProvider)
		return ok
	})
	disabled := r.configureOnly(found, configDir, sections)
	drivers := map[string]ToolDriver{}
	owners := map[string]string{}
	for _, style := range reserved {
		owners[style] = "the host"
	}
	var errs []error
	for _, i := range found {
		if disabled[r.ids[i]] != nil || !enabled(r.extensions[i]) {
			continue
		}
		for _, driver := range r.extensions[i].(ToolDriverProvider).ToolDrivers() {
			if v := reflect.ValueOf(driver); driver == nil || (v.Kind() == reflect.Pointer && v.IsNil()) {
				errs = append(errs, fmt.Errorf("extension %q supplied a nil tool driver", r.ids[i]))
				continue
			}
			style := driver.Style()
			if !idPattern.MatchString(style) {
				errs = append(errs, fmt.Errorf("extension %q supplied a tool driver styled %q, which must be lower case, start with a letter, and hold only letters, digits, '-' and '_'", r.ids[i], style))
				continue
			}
			if owner, taken := owners[style]; taken {
				errs = append(errs, fmt.Errorf("extension %q supplied a tool driver styled %q, which %s already provides", r.ids[i], style, owner))
				continue
			}
			owners[style] = "extension " + r.ids[i]
			drivers[style] = driver
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return drivers, nil
}

// providers are the indexes of the extensions is keeps.
func (r *Registry) providers(is func(Extension) bool) []int {
	var found []int
	for i, ext := range r.extensions {
		if is(ext) {
			found = append(found, i)
		}
	}
	return found
}

// configureOnly configures the extensions at found, from sections and under
// configDir, when the registry as a whole has not been: a CLI command asking
// for one capability has no use for the rest. It returns the extensions a
// refused section switched off, and why, as Configure records them: the
// registry's own record once it is configured.
func (r *Registry) configureOnly(found []int, configDir string, sections map[string]map[string]any) map[string]error {
	if r.configured {
		return r.disabled
	}
	disabled := map[string]error{}
	for _, i := range found {
		if err := configureOne(r.extensions[i], configDir, r.ids[i], sections); err != nil {
			disabled[r.ids[i]] = err
		}
	}
	return disabled
}

// serving reports whether the extension at i runs: its section was not
// refused, and it has not switched itself off.
func (r *Registry) serving(i int) bool {
	return r.disabled[r.ids[i]] == nil && enabled(r.extensions[i])
}

// DataDir is where the extension with id keeps its state under a config
// directory: <configDir>/extensions/<id>.
func DataDir(configDir, id string) string {
	return filepath.Join(configDir, "extensions", id)
}

// MCPResult is what one extension's MCP registration came to.
type MCPResult struct {
	ID      string
	Version string
	Tools   []string
	Err     error
}

// RegisterMCP asks every enabled MCPProvider its config did not disable, in
// order, to add its tools to server. reserved names the tools the host has already registered; an
// extension can never displace one of them, nor one another extension
// registered first.
//
// One extension failing is not the server failing: its tools are removed
// again, so a failed extension is indistinguishable from a switched-off one,
// and the rest carry on. The result reports each provider asked.
func (r *Registry) RegisterMCP(server *mcp.Server, session SessionContext, reserved []string) ([]MCPResult, error) {
	if !r.configured {
		return nil, errors.New("extensions must be configured before they register")
	}
	owners := map[string]string{}
	for _, name := range reserved {
		owners[name] = "the host"
	}
	var results []MCPResult
	for i, ext := range r.extensions {
		provider, ok := ext.(MCPProvider)
		if !ok || !r.serving(i) {
			continue
		}
		registrar := &Registrar{server: server, owner: r.ids[i], owners: owners}
		err := registerOne(provider, registrar, scopedSession(session, r.ids[i]))
		if err != nil {
			server.RemoveTools(registrar.added...)
			for _, name := range registrar.added {
				delete(owners, name)
			}
			registrar.added = nil
		}
		results = append(results, MCPResult{
			ID:      r.ids[i],
			Version: ext.Descriptor().Version,
			Tools:   registrar.added,
			Err:     err,
		})
	}
	return results, nil
}

// extensionScoped is a Host that can act for one extension: the host's own,
// which lends each extension a copy whose board plans carry its ID.
type extensionScoped interface {
	ForExtension(id string) Host
}

// registerOne turns a panic into an error. The SDK panics on a tool whose
// schema it cannot infer, and one extension's bad tool must not take the
// host's tools down with it.
func registerOne(provider MCPProvider, registrar *Registrar, session SessionContext) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panicked while registering: %v", recovered)
		}
	}()
	return provider.RegisterMCP(registrar, session)
}

// scopedSession is session as the extension with id is lent it: the Host's
// own copy for that extension when it lends one, with its log lines and
// spans tagged with id.
func scopedSession(session SessionContext, id string) SessionContext {
	if scoped, ok := session.Host.(extensionScoped); ok {
		session.Host = scoped.ForExtension(id)
	}
	if session.Host != nil {
		session.Host = scopedHost{Host: session.Host, id: id, logger: session.Host.Logger().With("extension", id)}
	}
	return session
}

type scopedHost struct {
	Host
	id     string
	logger *slog.Logger
}

func (h scopedHost) Logger() *slog.Logger { return h.logger }
func (h scopedHost) Tracer() Tracer       { return scopedTracer{Tracer: h.Host.Tracer(), id: h.id} }

func enabled(ext Extension) bool {
	if toggle, ok := ext.(Enabler); ok {
		return toggle.Enabled()
	}
	return true
}

func orNone(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

// Registrar is an extension's handle on the MCP server it is registering
// into. It only adds tools, and refuses a name already taken.
type Registrar struct {
	server *mcp.Server
	owner  string
	owners map[string]string
	added  []string
}

// AddTool registers a tool whose input and output schemas are inferred from
// In and Out, exactly as mcp.AddTool does. A name the host or an earlier
// extension already registered is refused.
func AddTool[In, Out any](r *Registrar, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) error {
	if tool == nil || strings.TrimSpace(tool.Name) == "" {
		return errors.New("a tool needs a name")
	}
	if owner, taken := r.owners[tool.Name]; taken {
		return fmt.Errorf("tool %q is already registered by %s", tool.Name, owner)
	}
	mcp.AddTool(r.server, tool, handler)
	r.owners[tool.Name] = "extension " + r.owner
	r.added = append(r.added, tool.Name)
	return nil
}

// BoardResult is what one extension's board start came to.
type BoardResult struct {
	ID      string
	Version string
	Err     error
}

// StartBoard asks every enabled BoardProvider its config did not disable,
// in order, to start. hostFor
// gives each one its own view of the board, and a release that takes back
// everything that view subscribed; a provider that fails or panics is
// released at once, and the rest carry on. The result reports each provider
// asked.
//
// stop stops every provider that started, in reverse order, and then
// releases it. A panicking stop is reported in the error rather than ending
// the others'.
func (r *Registry) StartBoard(ctx context.Context, hostFor func(id string) (BoardHost, func())) (results []BoardResult, stop func() error, err error) {
	if !r.configured {
		return nil, nil, errors.New("extensions must be configured before the board starts them")
	}
	type started struct {
		id      string
		stop    func()
		release func()
	}
	var running []started
	for i, ext := range r.extensions {
		provider, ok := ext.(BoardProvider)
		if !ok || !r.serving(i) {
			continue
		}
		host, release := hostFor(r.ids[i])
		stopOne, err := startOne(ctx, provider, host)
		if err != nil {
			release()
		} else {
			running = append(running, started{id: r.ids[i], stop: stopOne, release: release})
		}
		results = append(results, BoardResult{ID: r.ids[i], Version: ext.Descriptor().Version, Err: err})
	}
	stop = func() error {
		var errs []error
		for i := len(running) - 1; i >= 0; i-- {
			if err := stopOne(running[i].stop); err != nil {
				errs = append(errs, fmt.Errorf("extension %q: %w", running[i].id, err))
			}
			running[i].release()
		}
		return errors.Join(errs...)
	}
	return results, stop, nil
}

func startOne(ctx context.Context, provider BoardProvider, host BoardHost) (stop func(), err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			stop, err = nil, fmt.Errorf("panicked while starting: %v", recovered)
		}
	}()
	return provider.StartBoard(ctx, host)
}

func stopOne(stop func()) (err error) {
	if stop == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panicked while stopping: %v", recovered)
		}
	}()
	stop()
	return nil
}
