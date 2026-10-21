package ui

import (
	"os"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
)

// armOn opens the key map on the one binding a search for query leaves, and
// arms the capture on it.
func armOn(t *testing.T, m *Model, query string, want keymap.Action) {
	t.Helper()
	m.width, m.height = 200, 60
	m.openHelp()
	m.help.query = query
	m.help.searching = false
	m.frame()
	if row, ok := m.selectedBinding(); !ok || row.action != want {
		t.Fatalf("the search %q left the cursor on %v, want %s", query, row.action, want)
	}
	m.handleHelpKey(namedKey(tea.KeyEnter))
	if !m.help.capturing {
		t.Fatal("enter did not arm the capture")
	}
}

func notesText(m *Model) string { return strings.Join(m.help.notes, "\n") }

// A key another action holds is not taken on the press: the key map names the
// holder and waits, and only a yes moves the key -- off the holder, which is
// left with whatever else it had.
func TestRebindingOntoATakenKeyAsksFirstAndNamesTheHolder(t *testing.T) {
	m := buildModel(t)
	armOn(t, m, "show all of a session", keymap.ShowAllWork)
	m.handleHelpKey(runeKey("x"))

	if m.help.clash == nil {
		t.Fatalf("x was taken without asking; notes: %s", notesText(m))
	}
	if !strings.Contains(notesText(m), `x is already bound to "kill it and file the row" (archive)`) {
		t.Errorf("the warning does not name archive by its label:\n%s", notesText(m))
	}
	if got := m.km().Key(keymap.ContextList, keymap.Archive); got != "x" {
		t.Fatalf("archive lost x before the answer: now %q", got)
	}
	if !hintPairHas(m.helpHint(), "y/↵") || !hintPairHas(m.helpHint(), "esc/n") {
		t.Errorf("the hint does not offer the answers: %v", m.helpHint())
	}

	m.handleHelpKey(runeKey("y"))
	if m.help.clash != nil {
		t.Fatal("the question is still up after y")
	}
	if got := m.km().Key(keymap.ContextList, keymap.ShowAllWork); got != "x" {
		t.Errorf("show_all_work is on %q, want x", got)
	}
	if m.km().Bound(keymap.ContextList, keymap.Archive) {
		t.Errorf("archive still holds %v after giving x up", m.km().Keys(keymap.ContextList, keymap.Archive))
	}
	if !strings.Contains(notesText(m), "archive gave it up and is now on no key") {
		t.Errorf("the result does not say archive lost the key:\n%s", notesText(m))
	}
	raw, err := os.ReadFile(keymap.Path(m.configDir()))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^archive\s+= \[\]$`).Match(raw) ||
		!regexp.MustCompile(`(?m)^show_all_work\s+= \["x"\]$`).Match(raw) {
		t.Errorf("the move was not saved:\n%s", raw)
	}
}

// No, or esc, leaves both bindings exactly as they were.
func TestDecliningAClashChangesNothing(t *testing.T) {
	for _, answer := range []tea.KeyMsg{runeKey("n"), namedKey(tea.KeyEsc)} {
		m := buildModel(t)
		armOn(t, m, "show all of a session", keymap.ShowAllWork)
		m.handleHelpKey(runeKey("x"))
		m.handleHelpKey(answer)
		if m.help.clash != nil {
			t.Fatalf("%s left the question up", answer)
		}
		if got := m.km().Key(keymap.ContextList, keymap.Archive); got != "x" {
			t.Errorf("%s: archive is on %q", answer, got)
		}
		if m.km().Bound(keymap.ContextList, keymap.ShowAllWork) {
			t.Errorf("%s: show_all_work picked up %v", answer, m.km().Keys(keymap.ContextList, keymap.ShowAllWork))
		}
		if !strings.Contains(notesText(m), "x stays on archive") {
			t.Errorf("%s: notes = %s", answer, notesText(m))
		}
	}
}

// Yes is the confirm dialog's binding, read from the live map: moved to o,
// the prompt answers o, ignores y, and its footer says o.
func TestTheClashAnswersTheConfirmBinding(t *testing.T) {
	m := buildModel(t)
	m.keys, _ = m.km().Rebind(keymap.ContextConfirm, keymap.Confirm, []string{"o"})
	armOn(t, m, "show all of a session", keymap.ShowAllWork)
	m.handleHelpKey(runeKey("x"))
	if !hintPairHas(m.helpHint(), "o") {
		t.Errorf("the hint = %v, want o for yes", m.helpHint())
	}
	m.handleHelpKey(runeKey("y"))
	if m.help.clash == nil {
		t.Fatal("y answered a clash after confirm moved to o")
	}
	m.handleHelpKey(runeKey("o"))
	if got := m.km().Key(keymap.ContextList, keymap.ShowAllWork); got != "x" {
		t.Errorf("o did not move x: show_all_work is on %q", got)
	}
}

// The holder is read from the map as it is now, not from the defaults: once
// archive has moved to y, y is the clash and x is free.
func TestTheClashIsReadFromTheLiveMap(t *testing.T) {
	m := buildModel(t)
	m.keys, _ = m.km().Rebind(keymap.ContextList, keymap.Archive, []string{"y"})
	armOn(t, m, "show all of a session", keymap.ShowAllWork)
	m.handleHelpKey(runeKey("x"))
	if m.help.clash != nil {
		t.Fatalf("x is free once archive moved off it, but the key map asked: %s", notesText(m))
	}
	armOn(t, m, "show all of a session", keymap.ShowAllWork)
	m.handleHelpKey(runeKey("y"))
	if m.help.clash == nil || m.help.clash.owner != keymap.Archive {
		t.Fatalf("y did not clash with archive: %s", notesText(m))
	}
}

// An extension's key is a key like any other: binding onto it asks, names the
// extension's own label, and a yes takes it from the extension.
func TestAClashWithAnExtensionKeyNamesTheExtensionAction(t *testing.T) {
	m := buildModel(t)
	m.keys, _ = keymap.NewWith(nil, []keymap.Binding{{
		Context: keymap.ContextList, Action: "noop_mark", Keys: []string{"Z"}, Label: "record the row",
	}})
	armOn(t, m, "show all of a session", keymap.ShowAllWork)
	m.handleHelpKey(runeKey("Z"))
	if !strings.Contains(notesText(m), `Z is already bound to "record the row" (noop_mark)`) {
		t.Fatalf("the warning does not name the extension's action:\n%s", notesText(m))
	}
	m.handleHelpKey(namedKey(tea.KeyEnter))
	if got := m.km().Key(keymap.ContextList, keymap.ShowAllWork); got != "Z" {
		t.Errorf("show_all_work is on %q, want Z", got)
	}
	if m.km().Bound(keymap.ContextList, "noop_mark") {
		t.Errorf("noop_mark kept Z")
	}
}

// The last key of an action the screen cannot work without is refused on the
// press, not offered and then refused: q is quit's only key.
func TestTheLastKeyOfARequiredActionIsRefusedWithoutAsking(t *testing.T) {
	m := buildModel(t)
	armOn(t, m, "show all of a session", keymap.ShowAllWork)
	m.handleHelpKey(runeKey("q"))
	if m.help.clash != nil {
		t.Fatal("the key map offered to move quit's only key")
	}
	if !strings.Contains(notesText(m), "last key on quit") {
		t.Errorf("notes = %s", notesText(m))
	}
	if got := m.km().Key(keymap.ContextList, keymap.Quit); got != "q" {
		t.Errorf("quit is on %q", got)
	}
}

// A free key, or the key the action already has, binds on the press.
func TestAFreeKeyBindsWithoutAsking(t *testing.T) {
	m := buildModel(t)
	armOn(t, m, "show all of a session", keymap.ShowAllWork)
	m.handleHelpKey(runeKey("z"))
	if m.help.clash != nil {
		t.Fatalf("a free key asked: %s", notesText(m))
	}
	armOn(t, m, "show all of a session", keymap.ShowAllWork)
	m.handleHelpKey(runeKey("z"))
	if m.help.clash != nil {
		t.Fatalf("the action's own key asked: %s", notesText(m))
	}
}

func hintPairHas(hint [][2]string, key string) bool {
	for _, pair := range hint {
		if pair[0] == key {
			return true
		}
	}
	return false
}
