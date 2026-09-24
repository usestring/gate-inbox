package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/keymap"
)

// pressExtensionKey presses k on the list and runs whatever it asked for,
// the way the runtime would, feeding the result back.
func pressExtensionKey(t *testing.T, m *Model, k string) {
	t.Helper()
	_, cmd := m.handleKey(key(k))
	if cmd == nil {
		t.Fatalf("%s asked for nothing", k)
	}
	msg := cmd()
	if _, ok := msg.(extensionKeyDoneMsg); !ok {
		t.Fatalf("%s answered %T, want the extension's run", k, msg)
	}
	m.Update(msg)
}

// An extension's key is answered with the row it was pressed on, off the
// event loop, and a default that collides with a board key loses to it.
func TestAnExtensionKeyRunsWithTheSelectedRow(t *testing.T) {
	m := childModel(t)
	var got []Press
	m.InstallExtensions([]ExtensionUI{{Owner: "items", Keys: []ExtensionKey{
		{Action: "open_item", Keys: []string{"c"}, Label: "open the item",
			Run: func(p Press) error { got = append(got, p); return nil }},
		{Action: "grab_quit", Keys: []string{"q"}, Label: "wants quit's key",
			Run: func(Press) error { t.Error("an extension took quit's key"); return nil }},
	}}}, NewExtensionBridge([]string{"items"}))

	m.selectSessionRow(t, "unrelated")
	pressExtensionKey(t, m, "c")
	m.selectGroupRow(t, "research")
	pressExtensionKey(t, m, "c")
	want := []Press{{SessionID: "s9", Group: "research"}, {Group: "research"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("presses = %+v, want %+v", got, want)
	}
	if action, _ := m.km().Action(keymap.ContextList, "q"); action != keymap.Quit {
		t.Fatalf("q answers %q", action)
	}
	if !strings.Contains(strings.Join(m.keyProblems, "\n"), "grab_quit") {
		t.Fatalf("the collision was not reported: %v", m.keyProblems)
	}
}

// A key that fails says so on the status bar under the extension's name,
// and one that panics costs its own press rather than the board.
func TestAFailingExtensionKeyIsReportedNotFatal(t *testing.T) {
	m := childModel(t)
	m.InstallExtensions([]ExtensionUI{{Owner: "items", Keys: []ExtensionKey{
		{Action: "fails", Keys: []string{"c"}, Run: func(Press) error { return errors.New("no item here") }},
		{Action: "panics", Keys: []string{"C"}, Run: func(Press) error { panic("broken") }},
	}}}, NewExtensionBridge([]string{"items"}))
	m.selectSessionRow(t, "worker")

	pressExtensionKey(t, m, "c")
	if m.errBar.text != "items: no item here" {
		t.Fatalf("status bar = %q", m.errBar.text)
	}
	pressExtensionKey(t, m, "C")
	if !strings.HasPrefix(m.errBar.text, "items: panicked") {
		t.Fatalf("status bar = %q", m.errBar.text)
	}
}

// The operator's key file moves an extension's key like any other, and the
// key map screen lists it under the extension's name.
func TestTheKeyFileAndTheKeyMapReachExtensionKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, keymap.FileName), []byte("[list]\nopen_item = [\"C\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := childModel(t)
	m.hooks = hooks.NewManager(dir)
	var pressed int
	m.InstallExtensions([]ExtensionUI{{Owner: "items", Keys: []ExtensionKey{
		{Action: "open_item", Keys: []string{"c"}, Label: "open the item", Run: func(Press) error { pressed++; return nil }},
	}}}, NewExtensionBridge([]string{"items"}))
	if len(m.keyProblems) != 0 {
		t.Fatalf("problems: %v", m.keyProblems)
	}
	m.selectSessionRow(t, "worker")
	pressExtensionKey(t, m, "C")
	if pressed != 1 {
		t.Fatal("the rebound key did not run")
	}
	var section *resolvedSection
	for _, s := range m.resolvedHelp() {
		if s.title == "items" {
			section = &s
		}
	}
	if section == nil || len(section.rows) != 1 || section.rows[0].key != "C" || section.rows[0].text != "open the item" {
		t.Fatalf("key map section: %+v", section)
	}
}

// Badges an extension pushes land on the row after one message, in the
// order the build lists its extensions, and fall back to their short form
// and then off the row as it narrows.
func TestExtensionBadgesDrawOnTheRow(t *testing.T) {
	m := childModel(t)
	bridge := NewExtensionBridge([]string{"first", "second"})
	m.InstallExtensions(nil, bridge)
	// The runtime would deliver the message; the test delivers it itself.
	bridge.Attach(func(tea.Msg) {})

	bridge.Decorate("second", "s9", []Badge{{Text: "later", Tone: ToneBad}})
	bridge.Decorate("first", "s9", []Badge{{Text: "set 3/5 \x1b[31m·\n12m", Short: "3/5", Tone: ToneAccent}})
	m.Update(extensionBadgesMsg{})

	row := func(width int) string {
		m.width = width
		m.rebuildRows()
		for i, entry := range m.rows {
			if entry.isSession() && entry.sess.ID == "s9" {
				return ansi.Strip(m.renderTreeRow(entry, false, width, i, panelHex()))
			}
		}
		t.Fatal("no row for s9")
		return ""
	}
	wide := row(200)
	if !strings.Contains(wide, "set 3/5 [31m·12m later") {
		t.Fatalf("wide row = %q, want both badges in build order with the escape removed", wide)
	}
	for _, width := range []int{60, 50, 45, 40} {
		narrow := row(width)
		if strings.Contains(narrow, "later") && !strings.Contains(narrow, "3/5") {
			t.Fatalf("%d columns: %q draws the second badge without the first", width, narrow)
		}
	}
	if narrow := row(40); strings.Contains(narrow, "set 3/5") {
		t.Fatalf("40 columns kept the long form: %q", narrow)
	}

	bridge.Decorate("first", "s9", nil)
	bridge.Decorate("second", "s9", nil)
	m.Update(extensionBadgesMsg{})
	if strings.Contains(row(200), "later") {
		t.Fatal("cleared badges are still drawn")
	}
}

// A badge with rungs is drawn as the widest one the row has room for, in its
// spans' own tones, and is left off once even the narrowest does not fit.
func TestExtensionBadgeRungsNarrowWithTheRow(t *testing.T) {
	m := childModel(t)
	bridge := NewExtensionBridge([]string{"sup"})
	m.InstallExtensions(nil, bridge)
	bridge.Attach(func(tea.Msg) {})
	mark := Span{Text: "◈", Tone: ToneAccent, Bold: true}
	bridge.Decorate("sup", "s9", []Badge{{
		Rungs: [][]Span{
			{mark, {Text: " 2c · 3/h · 12m\x1b[31m"}},
			{mark, {Text: " 2c · 3/h"}},
			{{Text: " \n"}, mark, {Text: "  "}},
			{{Text: "\x07"}},
		},
		// Ignored: the rungs are set.
		Text: "shorthand", Tone: ToneBad,
	}})
	m.Update(extensionBadgesMsg{})

	row := func(width int) (plain, styled string) {
		m.width = width
		m.rebuildRows()
		for i, entry := range m.rows {
			if entry.isSession() && entry.sess.ID == "s9" {
				styled = m.renderTreeRow(entry, false, width, i, panelHex())
				return ansi.Strip(styled), styled
			}
		}
		t.Fatal("no row for s9")
		return "", ""
	}
	wide, styled := row(200)
	if !strings.Contains(wide, " ◈ 2c · 3/h · 12m[31m") || strings.Contains(wide, "shorthand") {
		t.Fatalf("wide row = %q, want the widest rung, cleaned, and not the shorthand", wide)
	}
	if !strings.Contains(styled, "\x1b[1;"+strings.TrimPrefix(accentStyle.pfx, "\x1b[")+"◈") {
		t.Fatalf("the mark is not drawn bold in the accent: %q", styled)
	}
	if !strings.Contains(styled, subtleStyle.pfx+" 2c · 3/h · 12m[31m") {
		t.Fatalf("the numbers are not drawn muted: %q", styled)
	}

	// Walk the row narrower: every width draws a rung or nothing, the
	// rungs step down in order, and the one that cleaned to nothing is
	// never drawn.
	seen := map[string]bool{}
	last := 0
	for width := 200; width >= 20; width-- {
		plain, _ := row(width)
		rung := 3
		switch {
		case strings.Contains(plain, "◈ 2c · 3/h · 12m"):
			rung = 0
		case strings.Contains(plain, "◈ 2c · 3/h"):
			rung = 1
		case strings.Contains(plain, "◈"):
			rung = 2
		}
		if rung < last {
			t.Fatalf("%d columns went back to a wider rung: %q", width, plain)
		}
		last = rung
		seen[fmt.Sprint(rung)] = true
	}
	for _, rung := range []string{"0", "1", "2", "3"} {
		if !seen[rung] {
			t.Fatalf("no width drew rung %s (3 is none); saw %v", rung, seen)
		}
	}
}

// The shorthand is two rungs of one tone, and a badge whose only rungs clean
// to nothing is dropped like a badge with no Text.
func TestExtensionBadgeShorthandIsTwoRungs(t *testing.T) {
	got := badgeRungs(Badge{Text: " run 3/5\n", Short: "3/5", Tone: ToneWarn})
	want := [][]Span{{{Text: "run 3/5", Tone: ToneWarn}}, {{Text: "3/5", Tone: ToneWarn}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rungs = %+v, want %+v", got, want)
	}
	if got := badgeRungs(Badge{Short: "3/5"}); got != nil {
		t.Fatalf("a badge with only a Short = %+v, want none", got)
	}
	if got := badgeRungs(Badge{Rungs: [][]Span{{{Text: "\x1b"}}, {{Text: "  "}}}}); len(got) != 0 {
		t.Fatalf("rungs that clean to nothing = %+v, want none", got)
	}
}
