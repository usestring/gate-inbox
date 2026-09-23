package ui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
)

// writeKeys puts a key file in the model's config directory and reloads it,
// which is what a board does at startup.
func writeKeys(t testing.TB, m *Model, text string) {
	t.Helper()
	if err := os.WriteFile(keymap.Path(m.configDir()), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	m.loadKeys()
}

// The whole feature from the operator's side: a line in keys.toml and the
// key that shows every artifact is the one they chose.
func TestAKeyFileMovesABinding(t *testing.T) {
	m := buildModel(t)
	writeKeys(t, m, "[list]\nshow_all_work = [\"z\"]\n")
	if problems := m.keyProblems; len(problems) > 0 {
		t.Fatalf("the file was refused: %v", problems)
	}

	m.handleKey(runeKey("z"))
	if !m.showAllWork {
		t.Fatal("z left the rail capped")
	}
	// And the key it moved off no longer answers.
	m.handleKey(runeKey("W"))
	if !m.showAllWork {
		t.Fatal("W still lifts the cap after the binding moved")
	}
}

// The footer follows the file. A legend that kept printing the default is
// the failure this whole change exists to prevent.
func TestTheFooterNamesTheReboundKey(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 200, 50
	writeKeys(t, m, "[list]\nnew_session = [\"z\"]\n")
	footer := ansi.Strip(m.viewFooter())
	if !strings.Contains(footer, "z new") {
		t.Errorf("the footer does not name z as new:\n%s", footer)
	}
}

// And so does the key map.
func TestTheKeyMapNamesTheReboundKey(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 200, 60
	m.mode = modeHelp
	writeKeys(t, m, "[list]\nnew_group = [\"alt+g\"]\n")
	if frame := ansi.Strip(m.frame()); !strings.Contains(frame, keymap.Display("alt+g")) {
		t.Errorf("the key map still shows the default:\n%s", frame)
	}
}

// H opens the full key map and closes the screen it opened.
func TestHOpensAndClosesTheKeyMap(t *testing.T) {
	m := buildModel(t)
	m.handleKey(runeKey("H"))
	if m.mode != modeHelp {
		t.Fatalf("H left the board in mode %v, want the key map", m.mode)
	}
	m.handleHelpKey(runeKey("H"))
	if m.mode == modeHelp {
		t.Fatal("H did not close the key map it opened")
	}
}

// A refused override is reported on the key map screen rather than swallowed:
// otherwise it is a key that does nothing, with the reason in a process
// nobody can see.
func TestARefusedOverrideIsReportedAndFallsBack(t *testing.T) {
	m := buildModel(t)
	writeKeys(t, m, "[list]\nnew_session = [\"ctrl+alt+d\"]\n")
	if len(m.keyProblems) == 0 {
		t.Fatal("binding into the snippets chord was accepted")
	}
	if got := m.km().Key(keymap.ContextList, keymap.NewSession); got != "n" {
		t.Fatalf("new_session fell back to %q", got)
	}
	m.mode = modeHelp
	m.width, m.height = 200, 60
	if frame := ansi.Strip(m.frame()); !strings.Contains(frame, "snippets file owns") {
		t.Errorf("the key map does not explain the refusal:\n%s", frame)
	}
}

// A file that cannot be parsed leaves the board on the defaults. Losing the
// keys because a key file has a typo in it is the one outcome that must not
// happen.
func TestABrokenKeyFileLeavesTheDefaultsWorking(t *testing.T) {
	m := buildModel(t)
	writeKeys(t, m, "[list\nnew_session = broken")
	if len(m.keyProblems) == 0 {
		t.Fatal("a broken file was read without complaint")
	}
	if got := m.km().Key(keymap.ContextList, keymap.NewSession); got != "n" {
		t.Fatalf("new_session is on %q after a broken file", got)
	}
}

// The in-app editor: land on a binding, press ↵, press the key, and it is
// bound and written to the file.
func TestRebindingFromTheKeyMapWritesTheFile(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 200, 60
	m.openHelp()
	m.help.query = "show all of a session"
	m.frame() // resolves the cursor onto the one row the search leaves

	row, ok := m.selectedBinding()
	if !ok {
		t.Fatal("the search left no binding under the cursor")
	}
	if row.action != keymap.ShowAllWork {
		t.Fatalf("the cursor is on %s", row.action)
	}
	m.handleHelpKey(namedKey(tea.KeyEnter))
	if !m.help.capturing {
		t.Fatal("enter did not arm the capture")
	}
	m.handleHelpKey(runeKey("z"))
	if m.help.capturing {
		t.Fatal("the capture is still armed after a key")
	}
	if got := m.km().Key(keymap.ContextList, keymap.ShowAllWork); got != "z" {
		t.Fatalf("show_all_work is on %q", got)
	}
	raw, err := os.ReadFile(keymap.Path(m.configDir()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `show_all_work = ["z"]`) {
		t.Fatalf("the rebind was not saved:\n%s", raw)
	}

	// And it survives a restart, which is the only proof that matters.
	m.loadKeys()
	if got := m.km().Key(keymap.ContextList, keymap.ShowAllWork); got != "z" {
		t.Fatalf("after a reload show_all_work is on %q", got)
	}
}

// esc during a capture leaves the binding alone. Every other key binds, so
// the one that means "I changed my mind" has to be read first.
func TestEscDuringACaptureChangesNothing(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 200, 60
	m.openHelp()
	m.help.query = "show all of a session"
	m.frame()
	m.handleHelpKey(namedKey(tea.KeyEnter))
	m.handleHelpKey(namedKey(tea.KeyEsc))
	if m.help.capturing {
		t.Fatal("esc left the capture armed")
	}
	if got := m.km().Key(keymap.ContextList, keymap.ShowAllWork); got != "W" {
		t.Fatalf("esc rebound show_all_work to %q", got)
	}
}

// r puts a binding back, which is the way out of a rebind somebody regrets.
func TestResetPutsABindingBackFromTheKeyMap(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 200, 60
	writeKeys(t, m, "[list]\nshow_all_work = [\"z\"]\n")
	m.openHelp()
	m.help.query = "show all of a session"
	m.frame()
	m.handleHelpKey(runeKey("r"))
	if got := m.km().Key(keymap.ContextList, keymap.ShowAllWork); got != "W" {
		t.Fatalf("reset left show_all_work on %q", got)
	}
	raw, err := os.ReadFile(keymap.Path(m.configDir()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "show_all_work") {
		t.Fatalf("the override was not cleared from the file:\n%s", raw)
	}
}

// A chord is recorded as the chord, not as the character the terminal
// reported for it. An enhanced keyboard protocol fills Text on alt+o, and a
// map that recorded "o" would bind the letter and eat it from the agent.
func TestACapturedChordIsRecordedAsAChord(t *testing.T) {
	if got := keyName(tea.KeyPressMsg{Code: 'o', Text: "o", Mod: tea.ModAlt}); got != "alt+o" {
		t.Fatalf("alt+o was read as %q", got)
	}
	if got := keyName(tea.KeyPressMsg{Code: 'x', Text: "x"}); got != "x" {
		t.Fatalf("a plain letter was read as %q", got)
	}
}

// The focused session's keys are rebindable too, and everything the map does
// not claim still reaches the agent.
func TestAReboundFocusKeyClaimsItsKey(t *testing.T) {
	m := buildModel(t)
	writeKeys(t, m, "[focus]\neditor = [\"alt+e\"]\n")
	if !m.isAction(keymap.ContextFocus, keymap.Editor,
		tea.KeyPressMsg{Code: 'e', Text: "e", Mod: tea.ModAlt}) {
		t.Fatal("alt+e does not open the editor after the rebind")
	}
	if m.isAction(keymap.ContextFocus, keymap.Editor,
		tea.KeyPressMsg{Code: 'o', Text: "o", Mod: tea.ModAlt}) {
		t.Fatal("alt+o still opens the editor")
	}
}

// A board with no key file writes the commented catalog, so the operator
// opening it finds every action they could bind.
func TestAFirstRunWritesTheReference(t *testing.T) {
	m := buildModel(t)
	if err := os.Remove(keymap.Path(m.configDir())); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	m.loadKeys()
	raw, err := os.ReadFile(keymap.Path(m.configDir()))
	if err != nil {
		t.Fatalf("no reference was written: %v", err)
	}
	for _, want := range []string{"# [list]", "# new_session", "# [focus]"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the reference does not mention %q:\n%s", want, raw)
		}
	}
	if len(m.keyProblems) > 0 {
		t.Fatalf("the reference it just wrote was refused: %v", m.keyProblems)
	}
}
