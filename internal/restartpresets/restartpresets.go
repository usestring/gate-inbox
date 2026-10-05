// Package restartpresets is the operator's own one-key restart flags:
// a user-editable file of key-and-args pairs, each of which restarts the
// session in front of you with those extra CLI flags appended.
//
// Restarting with different flags is the slowest part of giving a session a
// capability it was launched without -- most often Chrome, via claude's
// --chrome flag -- and every operator has a handful of their own flag sets.
// The file lives in the config directory beside snippets.json, is written
// once with a starting set, and is then the user's to edit. Nothing rewrites
// it.
//
// Presets fire from one menu, the restart-with-flags picker, which a list key
// opens over the session under the cursor. The menu is its own namespace: a
// bare key there names its preset, the way the hotkey menu's bare keys name
// snippets.
package restartpresets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Preset is one key and the extra flags it restarts with.
type Preset struct {
	// Key is the menu key: a single letter a-z.
	Key string `json:"key"`
	// Label is what the picker calls the preset. Empty falls back to Args.
	Label string `json:"label,omitempty"`
	// Args are extra CLI flags appended to the tool's launch command on
	// restart, e.g. "--chrome". Kept as one string so multi-flag sets read
	// as they would on a shell line.
	Args string `json:"args"`
}

// Title is what a surface should call this preset.
func (p Preset) Title() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Args
}

// Set is a loaded file: the presets that bound, and the entries that did not.
//
// Problems travel with the set rather than replacing it, because a rejected
// key is otherwise one that silently does nothing.
type Set struct {
	Presets  []Preset
	Problems []string
}

// Get returns the preset a keypress names.
func (s Set) Get(key string) (Preset, bool) {
	for _, preset := range s.Presets {
		if preset.Key == key {
			return preset, true
		}
	}
	return Preset{}, false
}

// Path is the restart flags file inside the config directory.
func Path(dir string) string { return filepath.Join(dir, "restart_flags.json") }

// Defaults are written on first run and are then the user's to edit. They
// are the flag set operators reach for most -- Chrome, for a session that
// needs the operator's signed-in browser -- because the file exists to be
// rewritten, and an empty one would not show what an entry looks like.
//
// Nothing rewrites a file that already exists, so an operator who has one
// adds a new default by hand.
func Defaults() []Preset {
	return []Preset{
		{Key: "c", Label: "with Chrome (--chrome)", Args: "--chrome"},
	}
}

// Load reads the restart flags file, writing the defaults first when it does
// not exist yet, and merges the distribution's entries under it: see
// UseDistribution.
//
// A file that exists but will not parse returns an error and NO presets
// rather than silently substituting the defaults.
func Load(dir string) (Set, error) {
	path := Path(dir)
	supplied := currentDistribution()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		defaults := firstRun(supplied)
		if encoded, err := json.MarshalIndent(defaults, "", "  "); err == nil {
			_ = os.WriteFile(path, append(encoded, '\n'), 0o644)
		}
		if len(supplied) == 0 {
			return Set{Presets: defaults}, nil
		}
		return validate(underlay(defaults, supplied)), nil
	}
	if err != nil {
		return Set{}, err
	}
	var parsed []Preset
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Set{}, fmt.Errorf("%s: %w", path, err)
	}
	return validate(underlay(parsed, supplied)), nil
}

// validate keeps the entries that can bind and says why the rest cannot.
func validate(parsed []Preset) Set {
	var set Set
	taken := map[string]int{}
	for i, preset := range parsed {
		preset.Key = strings.ToLower(strings.TrimSpace(preset.Key))
		preset.Label = strings.TrimSpace(preset.Label)
		preset.Args = strings.TrimSpace(preset.Args)
		switch {
		case preset.Key == "":
			set.Problems = append(set.Problems, entry(i, preset)+"has no key")
		case !legalKey(preset.Key):
			set.Problems = append(set.Problems,
				entry(i, preset)+"key "+quote(preset.Key)+" must be a single letter a-z")
		case preset.Args == "":
			set.Problems = append(set.Problems, entry(i, preset)+"carries no flags")
		default:
			if first, dup := taken[preset.Key]; dup {
				set.Problems = append(set.Problems,
					entry(i, preset)+"repeats "+preset.Key+", already bound by entry "+
						strconv.Itoa(first+1))
				continue
			}
			taken[preset.Key] = i
			set.Presets = append(set.Presets, preset)
		}
	}
	sort.Slice(set.Presets, func(a, b int) bool {
		return set.Presets[a].Key < set.Presets[b].Key
	})
	return set
}

func entry(index int, preset Preset) string {
	name := "entry " + strconv.Itoa(index+1)
	if preset.Label != "" {
		name += " (" + preset.Label + ")"
	}
	return name + ": "
}

func legalKey(key string) bool {
	runes := []rune(key)
	return len(runes) == 1 && runes[0] >= 'a' && runes[0] <= 'z'
}

func quote(s string) string { return "“" + s + "”" }
