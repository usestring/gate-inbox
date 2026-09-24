package config

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"

	"github.com/usestring/gate-inbox/internal/logging"
)

// A distribution's defaults are config.toml text of its own, laid under the
// operator's file every time a config is loaded. They are how a build for
// one estate fills in what DistributionSupplied leaves empty -- and anything
// else it wants its operators to start from -- without the core carrying it
// and without rewriting anybody's file.
//
// The order is: a key the operator's file defines wins, even when it defines
// it empty, which is how an operator turns a distribution default off; a key
// only the defaults define comes from them; and whatever is still unset
// falls back to the built-in blocks, exactly as it does with no defaults.
// Tables merge key by key; any other value, arrays included, is replaced
// whole.
var (
	defaultsMu sync.RWMutex
	defaults   map[string]any
)

// UseDefaults sets the distribution defaults every later load in this
// process lays under the operator's file. Empty text clears them. The text is
// checked here, once, so a malformed or misspelt overlay stops the build
// before any face of it runs rather than being ignored key by key. It returns
// a func restoring the previous defaults, for tests.
func UseDefaults(text string) (restore func(), err error) {
	table, err := parseDefaults(text)
	if err != nil {
		return nil, err
	}
	defaultsMu.Lock()
	defer defaultsMu.Unlock()
	previous := defaults
	defaults = table
	return func() {
		defaultsMu.Lock()
		defer defaultsMu.Unlock()
		defaults = previous
	}, nil
}

func parseDefaults(text string) (map[string]any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var cfg Config
	meta, err := toml.Decode(text, &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse config defaults: %w", err)
	}
	for _, section := range retiredSections {
		if meta.IsDefined(section) {
			return nil, fmt.Errorf("config defaults: [%s] is no longer read", section)
		}
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		return nil, fmt.Errorf("config defaults: unknown key(s): %s", strings.Join(keys, ", "))
	}
	var table map[string]any
	if _, err := toml.Decode(text, &table); err != nil {
		return nil, fmt.Errorf("parse config defaults: %w", err)
	}
	return table, nil
}

// DefaultExtensionSections names the [extensions.<id>] sections the current
// defaults carry, sorted. The defaults come with the build, so a section here
// that none of the build's extensions owns is the build's mistake, and is
// refused before any face runs rather than when a face configures.
func DefaultExtensionSections() []string {
	sections, _ := currentDefaults()["extensions"].(map[string]any)
	ids := make([]string, 0, len(sections))
	for id := range sections {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func currentDefaults() map[string]any {
	defaultsMu.RLock()
	defer defaultsMu.RUnlock()
	return defaults
}

// decodeInto reads the operator's file at path into cfg, over the
// distribution defaults when there are any.
func decodeInto(path string, cfg *Config) error {
	// The file is decoded on its own first, so a mistake in it is reported
	// against its own name and line whether or not defaults are laid under
	// it.
	meta, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return err
	}
	for _, section := range retiredSections {
		if meta.IsDefined(section) {
			logging.Warn("config section is no longer read; it is ignored", "path", path, "section", section)
		}
	}
	base := currentDefaults()
	if base == nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var own map[string]any
	if _, err := toml.Decode(string(data), &own); err != nil {
		return err
	}
	for _, section := range retiredSections {
		delete(own, section)
	}
	var merged bytes.Buffer
	if err := toml.NewEncoder(&merged).Encode(underlay(own, base)); err != nil {
		return fmt.Errorf("merge config defaults: %w", err)
	}
	*cfg = Config{}
	if _, err := toml.Decode(merged.String(), cfg); err != nil {
		return fmt.Errorf("merge config defaults: %w", err)
	}
	return nil
}

// underlay returns top with every key only base defines filled in from
// base, descending into tables both define. Neither map is changed.
func underlay(top, base map[string]any) map[string]any {
	out := make(map[string]any, len(top)+len(base))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range top {
		topTable, topIsTable := value.(map[string]any)
		baseTable, baseIsTable := base[key].(map[string]any)
		if topIsTable && baseIsTable {
			out[key] = underlay(topTable, baseTable)
			continue
		}
		out[key] = value
	}
	return out
}
