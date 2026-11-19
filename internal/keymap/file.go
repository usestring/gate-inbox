package keymap

import (
	"fmt"
	"maps"
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

// DeviceTable is the table in the key file that holds per-device layers:
// [device."<identity>".<screen>], where identity is the fingerprint
// tmux.DeviceIdentity gives the attached client. It is not a screen name, so
// no screen can ever be called this.
const DeviceTable = "device"

// File is the whole key file: the overrides every device shares, and per
// device the overrides that device layers on top of them. Precedence is the
// catalog's defaults, then Global, then the attached device's layer.
type File struct {
	Global  Overrides
	Devices map[string]Overrides
}

// For is the overrides one device resolves with: Global with that device's
// layer written over it, action by action. An unknown or empty device is
// Global alone.
func (f File) For(device string) Overrides {
	return Layer(f.Global, f.Devices[device])
}

// Layer is base with top written over it. An action top names takes top's
// keys whole, an empty list included; every other action keeps base's.
func Layer(base, top Overrides) Overrides {
	out := Overrides{}
	for _, layer := range []Overrides{base, top} {
		for ctx, set := range layer {
			if out[ctx] == nil {
				out[ctx] = map[Action][]string{}
			}
			for action, keys := range set {
				out[ctx][action] = append([]string{}, keys...)
			}
		}
	}
	return out
}

// Load reads the operator's shared overrides, without any device's layer.
// A missing file is not an error: it is the ordinary case, and it means the
// defaults.
func Load(dir string) (Overrides, error) {
	file, err := LoadFile(dir)
	return file.Global, err
}

// LoadFile reads the whole key file, device layers included.
func LoadFile(dir string) (File, error) {
	path := Path(dir)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return File{Global: Overrides{}}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read %s: %w", path, err)
	}
	return DecodeFile(string(raw))
}

// Decode parses the key file's text and returns its shared overrides. Split
// out so a test can read a file that was never written, and so the rebind
// screen can round-trip what it is about to save.
func Decode(text string) (Overrides, error) {
	file, err := DecodeFile(text)
	return file.Global, err
}

// DecodeFile parses the key file's text, device layers included.
func DecodeFile(text string) (File, error) {
	var raw map[string]toml.Primitive
	meta, err := toml.Decode(text, &raw)
	if err != nil {
		return File{}, err
	}
	file := File{Global: Overrides{}}
	for name, prim := range raw {
		if name == DeviceTable {
			var devices map[string]map[string]map[string][]string
			if err := meta.PrimitiveDecode(prim, &devices); err != nil {
				return File{}, fmt.Errorf("[%s]: %w", DeviceTable, err)
			}
			file.Devices = map[string]Overrides{}
			for device, screens := range devices {
				file.Devices[device] = toOverrides(screens)
			}
			continue
		}
		var actions map[string][]string
		if err := meta.PrimitiveDecode(prim, &actions); err != nil {
			return File{}, fmt.Errorf("[%s]: %w", name, err)
		}
		maps.Copy(file.Global, toOverrides(map[string]map[string][]string{name: actions}))
	}
	return file, nil
}

func toOverrides(raw map[string]map[string][]string) Overrides {
	out := Overrides{}
	for context, actions := range raw {
		set := map[Action][]string{}
		for action, keys := range actions {
			set[Action(action)] = keys
		}
		out[Context(context)] = set
	}
	return out
}

// Save writes the whole file, replacing whatever was there. Written whole
// rather than patched: the file is one table per screen with no state of its
// own, so rewriting it is how it stays readable after a dozen rebinds. It
// takes the File rather than one layer so a rebind on one device can never
// cost another device its layer.
func Save(dir string, file File) error {
	path := Path(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(EncodeFile(file)), 0o644); err != nil {
		return err
	}
	// Renamed into place so a board reading the file while another writes it
	// sees one version or the other, never half of one.
	return os.Rename(tmp, path)
}

// Encode renders shared overrides as the file's text.
func Encode(overrides Overrides) string {
	return EncodeFile(File{Global: overrides})
}

// EncodeFile renders the whole file: the shared tables, then each device's
// layer under [device."<identity>".<screen>], devices in name order.
func EncodeFile(file File) string {
	var b strings.Builder
	b.WriteString(header)
	writeTables(&b, "", file.Global)
	devices := make([]string, 0, len(file.Devices))
	for device := range file.Devices {
		devices = append(devices, device)
	}
	slices.Sort(devices)
	for _, device := range devices {
		writeTables(&b, DeviceTable+"."+strconv.Quote(device)+".", file.Devices[device])
	}
	return b.String()
}

func writeTables(b *strings.Builder, prefix string, overrides Overrides) {
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
		b.WriteString("\n[" + prefix + string(ctx) + "]\n")
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
			fmt.Fprintf(b, "%-*s = [%s]\n", width, string(action), strings.Join(keys, ", "))
		}
	}
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
#
# A table named [device."<identity>".<screen>] applies on one device only, on
# top of the tables above: the identity is the one Settings and the key map
# print for the device attached now. Rebinding from the key map writes to the
# attached device's tables when the board knows the device.
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
