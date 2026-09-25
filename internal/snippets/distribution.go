package snippets

import (
	"fmt"
	"strings"
	"sync"
)

// A distribution's snippets are entries a build supplies for its operators,
// merged by key under the operator's own file every time the file is loaded.
// They are never written to disk: the file stays the operator's, and a
// default the distribution changes or drops reaches every operator who never
// bound that key themselves.
//
// The order is: an entry the operator's file has for a key wins, whatever it
// says, so binding the key to something else is how an operator replaces a
// distribution's snippet; a key only the distribution supplies comes from it;
// and the built-in starting set is what a first run writes, less any key the
// distribution supplies, so the distribution's entry is not shadowed by a
// file the operator never wrote by hand.
var (
	distributionMu sync.RWMutex
	distribution   []Snippet
)

// UseDistribution sets the entries every later Load in this process merges
// under the operator's file. Nil or empty clears them. The entries are
// checked here, once, so one that could never bind stops the build before
// any face of it runs rather than surfacing as a Problem on every board. It
// returns a func restoring the previous entries, for tests.
func UseDistribution(entries []Snippet) (restore func(), err error) {
	var checked []Snippet
	if len(entries) > 0 {
		set := validate(entries)
		if len(set.Problems) > 0 {
			return nil, fmt.Errorf("snippet defaults: %s", strings.Join(set.Problems, "; "))
		}
		checked = set.Snippets
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

func currentDistribution() []Snippet {
	distributionMu.RLock()
	defer distributionMu.RUnlock()
	return distribution
}

// firstRun is what a first run writes: the built-in set, less the keys the
// distribution supplies.
func firstRun(supplied []Snippet) []Snippet {
	taken := keysOf(supplied)
	var out []Snippet
	for _, snip := range Defaults() {
		if !taken[snip.Key] {
			out = append(out, snip)
		}
	}
	return out
}

// underlay adds each distribution entry whose key no entry of the operator's
// file names. An operator entry counts even when it cannot bind: it is theirs,
// and the Problem it raises says why the key does nothing.
func underlay(own []Snippet, supplied []Snippet) []Snippet {
	if len(supplied) == 0 {
		return own
	}
	taken := keysOf(own)
	merged := append([]Snippet(nil), own...)
	for _, snip := range supplied {
		if !taken[snip.Key] {
			merged = append(merged, snip)
		}
	}
	return merged
}

func keysOf(entries []Snippet) map[string]bool {
	keys := make(map[string]bool, len(entries))
	for _, snip := range entries {
		keys[normalKey(snip.Key)] = true
	}
	return keys
}

func normalKey(key string) string { return strings.ToLower(strings.TrimSpace(key)) }
