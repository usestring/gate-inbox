package extension

import (
	"errors"
	"fmt"
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
// and its own data directory under configDir, and refuses a section no
// registered extension owns: a section for an
// extension this build does not carry is either a typo or a config written
// for a different build, and neither should pass silently.
//
// Every extension is configured even when an earlier one fails, so one run
// reports every problem.
func (r *Registry) Configure(configDir string, sections map[string]map[string]any) error {
	var errs []error
	var unknown []string
	for id := range sections {
		if !slices.Contains(r.ids, id) {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		errs = append(errs, fmt.Errorf("[extensions] has section(s) no extension in this build owns: %s (this build has: %s)",
			strings.Join(unknown, ", "), orNone(r.ids)))
	}
	for i, ext := range r.extensions {
		cfg := NewConfig(sections[r.ids[i]])
		if configDir != "" {
			cfg = cfg.WithDataDir(DataDir(configDir, r.ids[i]))
		}
		if err := ext.Configure(cfg); err != nil {
			errs = append(errs, fmt.Errorf("[extensions.%s]: %w", r.ids[i], err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	r.configured = true
	return nil
}

// Configured reports whether Configure has succeeded.
func (r *Registry) Configured() bool {
	return r.configured
}

// AccountPool is the pool the enabled AccountPoolProvider supplies, or nil
// when no extension in this build supplies one. On a registry nothing has
// configured yet, only the providers are configured, from sections and under
// configDir: a CLI command routing one launch has no use for the rest.
// Two enabled providers are refused rather than one silently winning.
func (r *Registry) AccountPool(configDir string, sections map[string]map[string]any) (AccountPool, error) {
	found := r.providers(func(ext Extension) bool {
		_, ok := ext.(AccountPoolProvider)
		return ok
	})
	if err := r.configureOnly(found, configDir, sections); err != nil {
		return nil, err
	}
	var pool AccountPool
	var owners []string
	for _, i := range found {
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
	return pool, nil
}

// ToolDrivers are the drivers every enabled ToolDriverProvider supplies,
// keyed by style. As with AccountPool, a registry nothing has configured yet
// configures only the providers. reserved names the styles the host
// implements itself; a driver can take none of them, nor a style another
// driver took first.
func (r *Registry) ToolDrivers(configDir string, sections map[string]map[string]any, reserved []string) (map[string]ToolDriver, error) {
	found := r.providers(func(ext Extension) bool {
		_, ok := ext.(ToolDriverProvider)
		return ok
	})
	if err := r.configureOnly(found, configDir, sections); err != nil {
		return nil, err
	}
	drivers := map[string]ToolDriver{}
	owners := map[string]string{}
	for _, style := range reserved {
		owners[style] = "the host"
	}
	var errs []error
	for _, i := range found {
		if !enabled(r.extensions[i]) {
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
// for one capability has no use for the rest.
func (r *Registry) configureOnly(found []int, configDir string, sections map[string]map[string]any) error {
	if r.configured {
		return nil
	}
	var errs []error
	for _, i := range found {
		cfg := NewConfig(sections[r.ids[i]])
		if configDir != "" {
			cfg = cfg.WithDataDir(DataDir(configDir, r.ids[i]))
		}
		if err := r.extensions[i].Configure(cfg); err != nil {
			errs = append(errs, fmt.Errorf("[extensions.%s]: %w", r.ids[i], err))
		}
	}
	return errors.Join(errs...)
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

// RegisterMCP asks every enabled MCPProvider, in order, to add its tools to
// server. reserved names the tools the host has already registered; an
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
		if !ok || !enabled(ext) {
			continue
		}
		registrar := &Registrar{server: server, owner: r.ids[i], owners: owners}
		err := registerOne(provider, registrar, session)
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
