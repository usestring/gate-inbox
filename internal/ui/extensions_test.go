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
	bridge := NewExtensionBridge([]string{"ext"})
	m.InstallExtensions(nil, bridge)
	bridge.Attach(func(tea.Msg) {})
	mark := Span{Text: "◈", Tone: ToneAccent, Bold: true}
	bridge.Decorate("ext", "s9", []Badge{{
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
	got := badgeRungs(Badge{Text: " set 3/5\n", Short: "3/5", Tone: ToneWarn})
	want := [][]Span{{{Text: "set 3/5", Tone: ToneWarn}}, {{Text: "3/5", Tone: ToneWarn}}}
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

// An extension's warning holds the open key on its row: the first press shows
// the warning and opens nothing, a second on the same row opens it, and esc
// or any other key between the two drops the hold. A row with no warning
// opens on the first press.
func TestAnExtensionWarningHoldsTheOpenKey(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "held", dir, "")
	createSession(t, m, "free", dir, "")
	m.focusOnEnter = true
	bridge := NewExtensionBridge([]string{"level"})
	m.InstallExtensions([]ExtensionUI{{Owner: "level"}}, bridge)
	bridge.ConfirmOpen("level", storedSession(t, m, "held").ID, "someone else is driving\x1b[31m")

	press := func(k string) {
		t.Helper()
		_, cmd := m.handleKey(key(k))
		m.applyCmd(t, cmd)
	}
	wantHeld := func(when string) {
		t.Helper()
		if m.mode != modeList {
			t.Fatalf("%s: mode %v, want the list", when, m.mode)
		}
	}
	const warning = "someone else is driving[31m — ↵ again to open, esc to cancel"

	m.selectSessionRow(t, "held")
	press("enter")
	wantHeld("first enter")
	if m.errBar.text != warning {
		t.Fatalf("status bar = %q, want %q", m.errBar.text, warning)
	}
	press("esc")
	wantHeld("esc")
	if m.errBar.text != "" {
		t.Fatalf("esc left %q on the status bar", m.errBar.text)
	}
	press("enter")
	wantHeld("enter after esc")

	// Another key between the two presses drops the hold, so coming back to
	// the row starts over.
	press("down")
	if m.openHeld != "" || m.errBar.text != "" {
		t.Fatalf("down kept the hold: held %q, status bar %q", m.openHeld, m.errBar.text)
	}
	m.selectSessionRow(t, "held")
	press("enter")
	wantHeld("enter after moving away and back")

	press("enter")
	if m.mode != modeFocus {
		t.Fatalf("second enter left mode %v, err = %q", m.mode, m.errBar.text)
	}
	m.leaveFocusForFixture(t)

	m.selectSessionRow(t, "free")
	press("enter")
	if m.mode != modeFocus {
		t.Fatalf("a row with no warning did not open at once: mode %v, err = %q", m.mode, m.errBar.text)
	}
	m.leaveFocusForFixture(t)

	// Clearing the warning lets its row open at once again.
	bridge.ConfirmOpen("level", storedSession(t, m, "held").ID, "")
	m.selectSessionRow(t, "held")
	press("enter")
	if m.mode != modeFocus {
		t.Fatalf("a cleared warning still held the row: mode %v, err = %q", m.mode, m.errBar.text)
	}
}

// Every extension's warning for a row is shown, in build order.
func TestOpenWarningsJoinInBuildOrder(t *testing.T) {
	bridge := NewExtensionBridge([]string{"items", "level"})
	bridge.ConfirmOpen("level", "s1", "second")
	bridge.ConfirmOpen("items", "s1", "first")
	bridge.ConfirmOpen("items", "s2", "other row")
	if got := bridge.openWarning("s1"); got != "first; second" {
		t.Fatalf("openWarning = %q", got)
	}
	bridge.ConfirmOpen("items", "s1", "  ")
	if got := bridge.openWarning("s1"); got != "second" {
		t.Fatalf("after clearing items: %q", got)
	}
}

// placementRow is s9's row at width, stripped of styling.
func placementRow(t *testing.T, m *Model, width int) string {
	t.Helper()
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

// A badge placed after the name is drawn right after it on a wide row, and
// several such badges keep build order there while a default badge stays in
// the slot at the end.
func TestABadgePlacedAfterTheNameFollowsIt(t *testing.T) {
	for _, stacked := range []bool{false, true} {
		t.Run(fmt.Sprint("stacked=", stacked), func(t *testing.T) {
			m := childModel(t)
			m.comfortableRows = stacked
			bridge := NewExtensionBridge([]string{"items", "tally", "level"})
			m.InstallExtensions(nil, bridge)
			bridge.Attach(func(tea.Msg) {})
			bridge.Decorate("level", "s9", []Badge{{Text: "second", AfterName: true}})
			bridge.Decorate("tally", "s9", []Badge{{Text: "slot"}})
			bridge.Decorate("items", "s9", []Badge{{Text: "first", AfterName: true}})
			m.Update(extensionBadgesMsg{})

			row := placementRow(t, m, 200)
			if !strings.Contains(row, "unrelated first second") {
				t.Fatalf("row = %q, want both badges right after the name, in build order", row)
			}
			if !strings.Contains(row, " slot") || strings.Index(row, "slot") < strings.Index(row, "second") {
				t.Fatalf("row = %q, want the default badge in the slot at the end", row)
			}
			if strings.Count(row, "first") != 1 {
				t.Fatalf("row = %q draws the badge twice", row)
			}
		})
	}
}

// On a row too narrow for it after the name, the badge first tries its
// narrower rungs there, then falls back to the slot and narrows there as a
// default badge does. The name is never cut for it.
func TestABadgePlacedAfterTheNameFallsBackToTheSlot(t *testing.T) {
	m := childModel(t)
	bridge := NewExtensionBridge([]string{"items"})
	m.InstallExtensions(nil, bridge)
	bridge.Attach(func(tea.Msg) {})
	bridge.Decorate("items", "s9", []Badge{{
		Rungs:     [][]Span{{{Text: "items 2 of 5 open"}}, {{Text: "i2/5"}}},
		AfterName: true,
	}})
	m.Update(extensionBadgesMsg{})

	if row := placementRow(t, m, 200); !strings.Contains(row, "unrelated items 2 of 5 open") {
		t.Fatalf("wide row = %q", row)
	}
	narrowed := false
	for width := 200; width >= 20; width-- {
		row := placementRow(t, m, width)
		if !strings.Contains(row, "unrelated") {
			t.Fatalf("%d columns cut the name: %q", width, row)
		}
		if strings.Contains(row, "unrelated i2/5") {
			narrowed = true
		}
	}
	if !narrowed {
		t.Fatal("no width drew the narrower rung after the name")
	}

	// A comfortable row puts the slot on the meta line. With a name long
	// enough that the name line has less room than the meta line, some
	// width has no room for the badge after the name, and it is drawn in
	// the slot instead, narrowed there. Wherever it follows the name, the
	// name is whole.
	m.comfortableRows = true
	long := "unrelated-with-a-name-long-enough-to-fill-its-line"
	for i := range m.sessions {
		if m.sessions[i].ID == "s9" {
			m.sessions[i].Name = long
		}
	}
	fellBack := false
	for width := 200; width >= 20; width-- {
		head, meta, _ := strings.Cut(placementRow(t, m, width), "\n")
		if strings.Contains(head, "i2/5") || strings.Contains(head, "items 2") {
			if !strings.Contains(head, long+" i") {
				t.Fatalf("%d columns cut the name for the badge: %q", width, head)
			}
			continue
		}
		if strings.Contains(meta, "i2/5") {
			fellBack = true
		}
	}
	if !fellBack {
		t.Fatal("no width sent the badge back to the slot")
	}
}

// With no placement, a badge is drawn in the slot exactly as before.
func TestADefaultPlacedBadgeStaysInTheSlot(t *testing.T) {
	m := childModel(t)
	bridge := NewExtensionBridge([]string{"tally", "items"})
	m.InstallExtensions(nil, bridge)
	bridge.Attach(func(tea.Msg) {})
	bridge.Decorate("tally", "s9", []Badge{{Text: "slot"}})
	bridge.Decorate("items", "s9", []Badge{{Text: "items"}})
	m.Update(extensionBadgesMsg{})
	if row := placementRow(t, m, 200); !strings.Contains(row, "unrelated slot items") {
		t.Fatalf("default row = %q, want both badges in the slot in build order", row)
	}

	bridge.Decorate("items", "s9", []Badge{{Text: "items", AfterName: true}})
	m.Update(extensionBadgesMsg{})
	if row := placementRow(t, m, 200); !strings.Contains(row, "unrelated items slot") {
		t.Fatalf("after-name row = %q, want the placed badge ahead of the slot", row)
	}
}
