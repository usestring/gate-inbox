package compat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/snippets"
)

// TestDefaultKeyBindings records every action the board answers, per
// context and in the order the key map screen lists them: its keys, its
// label, and whether it may be left unbound. It also records the commented
// keys.toml a first run writes.
func TestDefaultKeyBindings(t *testing.T) {
	s := newScratch(t)
	m, problems := keymap.New(keymap.Overrides{})
	if len(problems) > 0 {
		t.Fatalf("the defaults have problems: %v", problems)
	}
	golden(t, "keys/defaults.golden", describeKeys(m))

	if err := keymap.WriteReferenceIfMissing(s.home); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(keymap.Path(s.home))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "keys/first-run-keys.toml", string(raw))
}

// TestKeyOverrides records what a hand-edited keys.toml resolves to: a
// rebind, an unbind, an attempt to unbind a required action, a key two
// actions claim, and names no release knows.
func TestKeyOverrides(t *testing.T) {
	s := newScratch(t)
	raw, err := os.ReadFile(filepath.Join("testdata", "input", "keys.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s.writeFile(t, keymap.FileName, string(raw))
	overrides, err := keymap.Load(s.home)
	if err != nil {
		t.Fatal(err)
	}
	m, problems := keymap.New(overrides)
	var b strings.Builder
	b.WriteString("problems:\n")
	for _, problem := range problems {
		fmt.Fprintf(&b, "  %s\n", problem.Error())
	}
	b.WriteString("\nresolved:\n")
	b.WriteString(describeKeys(m))
	golden(t, "keys/overrides.golden", b.String())
}

func describeKeys(m *keymap.Map) string {
	var b strings.Builder
	for _, ctx := range keymap.Contexts {
		fmt.Fprintf(&b, "[%s]\n", ctx)
		for _, binding := range keymap.Catalog {
			if binding.Context != ctx {
				continue
			}
			keys := m.Keys(ctx, binding.Action)
			quoted := make([]string, len(keys))
			for i, key := range keys {
				quoted[i] = fmt.Sprintf("%q", key)
			}
			required := ""
			if binding.Required {
				required = " (required)"
			}
			fmt.Fprintf(&b, "%s = [%s]  # %s%s\n", binding.Action, strings.Join(quoted, ", "), binding.Label, required)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// TestSnippets records the snippets file a first run writes and what a
// hand-edited one with entries that cannot bind resolves to.
func TestSnippets(t *testing.T) {
	var b strings.Builder
	for _, scenario := range []struct{ name, input string }{
		{name: "absent"},
		{name: "edited", input: "snippets.json"},
	} {
		s := newScratch(t)
		if scenario.input != "" {
			raw, err := os.ReadFile(filepath.Join("testdata", "input", scenario.input))
			if err != nil {
				t.Fatal(err)
			}
			s.writeFile(t, "snippets.json", string(raw))
		}
		set, err := snippets.Load(s.home)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "=== %s\n", scenario.name)
		for _, snip := range set.Snippets {
			fmt.Fprintf(&b, "%s  %q -> %q\n", snip.Binding(), snip.Title(), snip.Text)
		}
		for _, problem := range set.Problems {
			fmt.Fprintf(&b, "problem: %s\n", problem)
		}
		if scenario.input == "" {
			written, err := os.ReadFile(snippets.Path(s.home))
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "--- snippets.json as written\n%s", written)
		}
		b.WriteString("\n")
	}
	golden(t, "keys/snippets.golden", b.String())
}
