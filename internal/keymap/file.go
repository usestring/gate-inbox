package keymap

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the operator's key file, kept beside config.toml. It is its
// own file rather than a table in config.toml for one reason: the rebind
// screen writes it. A screen that rewrote config.toml would have to
// reproduce every comment and every tool block the operator put there, and
// the first time it got that wrong it would cost them their configuration.
// This file is small, it is generated, and losing it costs nothing but the
// rebinds it holds.
const FileName = "keys.toml"

// Path is where the key file lives for a config directory.
func Path(dir string) string { return filepath.Join(dir, FileName) }

// Load reads the operator's overrides. A missing file is not an error: it is
// the ordinary case, and it means the defaults.
func Load(dir string) (Overrides, error) {
	path := Path(dir)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Overrides{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Decode(string(raw))
}

// Decode parses the key file's text. Split out so a test can read a file
// that was never written, and so the rebind screen can round-trip what it is
// about to save.
func Decode(text string) (Overrides, error) {
	var raw map[string]map[string][]string
	if _, err := toml.Decode(text, &raw); err != nil {
		return nil, err
	}
	out := Overrides{}
	for context, actions := range raw {
		set := map[Action][]string{}
		for action, keys := range actions {
			set[Action(action)] = keys
		}
		out[Context(context)] = set
	}
	return out, nil
}

// Save writes the overrides, replacing whatever was there. Written whole
// rather than patched: the file is one table per screen with no state of its
// own, so rewriting it is how it stays readable after a dozen rebinds.
func Save(dir string, overrides Overrides) error {
	path := Path(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(Encode(overrides)), 0o644); err != nil {
		return err
	}
	// Renamed into place so a board reading the file while another writes it
	// sees one version or the other, never half of one.
	return os.Rename(tmp, path)
}

// Encode renders overrides as the file's text.
func Encode(overrides Overrides) string {
	var b strings.Builder
	b.WriteString(header)
	// The catalog's screens first, in its order, then any other screen the
	// overrides name, an extension's, in name order.
	contexts := append([]Context(nil), Contexts...)
	for _, ctx := range sortedContexts(overrides) {
		if !slices.Contains(contexts, ctx) {
			contexts = append(contexts, ctx)
		}
	}
	for _, ctx := range contexts {
		set := overrides[ctx]
		if len(set) == 0 {
			continue
		}
		b.WriteString("\n[" + string(ctx) + "]\n")
		actions := sortedActions(set)
		width := 0
		for _, action := range actions {
			if len(action) > width {
				width = len(action)
			}
		}
		for _, action := range actions {
			keys := make([]string, 0, len(set[action]))
			for _, key := range set[action] {
				keys = append(keys, strconv.Quote(key))
			}
			fmt.Fprintf(&b, "%-*s = [%s]\n", width, string(action), strings.Join(keys, ", "))
		}
	}
	return b.String()
}

const header = `# Gate Inbox key bindings.
#
# One table per screen, one line per action: the keys it answers to. What is
# not written here keeps its default, and an empty list unbinds an action
# that is not required. Key names are the ones the key map screen prints:
# plain letters, "enter", "esc", "alt+o", "ctrl+q".
#
# Press ? in the board for the whole list of actions, and rebind from there
# rather than by hand if you would rather not look a name up. Anything this
# file gets wrong is reported on the board at startup and falls back to its
# default, so a typo here never costs you the list.
`

// Reference is the whole catalog as a commented file, which is what a first
// run writes: the operator opening keys.toml finds every action they could
// bind rather than an empty file.
func Reference() string {
	var b strings.Builder
	b.WriteString(header)
	for _, ctx := range Contexts {
		b.WriteString("\n# [" + string(ctx) + "]\n")
		width := 0
		for _, binding := range Catalog {
			if binding.Context == ctx && len(binding.Action) > width {
				width = len(binding.Action)
			}
		}
		for _, binding := range Catalog {
			if binding.Context != ctx {
				continue
			}
			keys := make([]string, 0, len(binding.Keys))
			for _, key := range binding.Keys {
				keys = append(keys, strconv.Quote(key))
			}
			note := "  # " + binding.Label
			if binding.Required {
				note += " (cannot be unbound)"
			}
			fmt.Fprintf(&b, "# %-*s = [%s]%s\n", width, string(binding.Action),
				strings.Join(keys, ", "), note)
		}
	}
	return b.String()
}

// WriteReferenceIfMissing puts the commented catalog in place the first time
// a board starts with no key file. Nothing rewrites it afterwards: from then
// on it is the operator's, and Save only ever replaces a file they or the
// rebind screen wrote.
func WriteReferenceIfMissing(dir string) error {
	path := Path(dir)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(Reference()), 0o644)
}
