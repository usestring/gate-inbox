package restartpresets

import (
	"fmt"
	"strings"
	"sync"
)

// A distribution's presets are entries a build supplies for its operators,
// merged by key under the operator's own file every time the file is loaded.
// They are never written to disk: the file stays the operator's, and a
// default the distribution changes or drops reaches every operator who never
// bound that key themselves.
var (
	distributionMu sync.RWMutex
	distribution   []Preset
)

// UseDistribution sets the entries every later Load in this process merges
// under the operator's file. Nil or empty clears them. The entries are
// checked here, once, so one that could never bind stops the build before
// any face of it runs rather than surfacing as a Problem on every board. It
// returns a func restoring the previous entries, for tests.
func UseDistribution(entries []Preset) (restore func(), err error) {
	var checked []Preset
	if len(entries) > 0 {
		set := validate(entries)
		if len(set.Problems) > 0 {
			return nil, fmt.Errorf("restart flag defaults: %s", strings.Join(set.Problems, "; "))
		}
		checked = set.Presets
	}
	distributionMu.Lock()
	defer distributionMu.Unlock()
	previous := distribution
	distribution = checked
	return func() {
		distributionMu.Lock()
		defer distributionMu.Unlock()
		distribution = previous
	}, nil
}

func currentDistribution() []Preset {
	distributionMu.RLock()
	defer distributionMu.RUnlock()
	return distribution
}

// firstRun is what a first run writes: the built-in set, less the keys the
// distribution supplies.
func firstRun(supplied []Preset) []Preset {
	taken := keysOf(supplied)
	var out []Preset
	for _, preset := range Defaults() {
		if !taken[preset.Key] {
			out = append(out, preset)
		}
	}
	return out
}

// underlay adds each distribution entry whose key no entry of the operator's
// file names.
func underlay(own []Preset, supplied []Preset) []Preset {
	if len(supplied) == 0 {
		return own
	}
	taken := keysOf(own)
	merged := append([]Preset(nil), own...)
	for _, preset := range supplied {
		if !taken[preset.Key] {
			merged = append(merged, preset)
		}
	}
	return merged
}

func keysOf(entries []Preset) map[string]bool {
	keys := make(map[string]bool, len(entries))
	for _, preset := range entries {
		keys[normalKey(preset.Key)] = true
	}
	return keys
}

func normalKey(key string) string { return strings.ToLower(strings.TrimSpace(key)) }
