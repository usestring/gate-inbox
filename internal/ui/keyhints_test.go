package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/namesweep"
)

// The reported bug: the archived view moved onto y, and the archived view
// itself still said t was the way back. Every place that names the way to it
// follows the rebind.
func TestRebindingTheArchivedViewMovesEveryHintToIt(t *testing.T) {
	m := buildModel(t)
	writeKeys(t, m, "[list]\narchived_view = [\"y\"]\n")
	if len(m.keyProblems) > 0 {
		t.Fatalf("the file was refused: %v", m.keyProblems)
	}
	m.width, m.height = 120, 40
	m.showArchived = true

	rail := ansi.Strip(railLinesText(m.railLines(36, m.listBodyHeight())))
	if !strings.Contains(rail, "ARCHIVED") || !strings.Contains(rail, "y back to active") {
		t.Errorf("the archived badge does not name y:\n%s", rail)
	}
	if strings.Contains(rail, "t back to active") {
		t.Errorf("the archived badge still names t:\n%s", rail)
	}
	empty := ansi.Strip(strings.Join(m.emptyRailLines(36, 10), "\n"))
	if !strings.Contains(empty, "y back to active") {
		t.Errorf("the empty archive does not name y:\n%s", empty)
	}
	if refusal := m.listSortRefusal(); !strings.Contains(refusal, "press y to go back") {
		t.Errorf("the reorder refusal = %q, want it to name y", refusal)
	}
	if !legendPair(m.viewLegend(), "y") {
		t.Error("the key peek does not offer y")
	}

	m.showArchived = false
	createSession(t, m, "alpha", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	m.archiveSelected()
	if !strings.Contains(m.confirm.label, "y finds it") {
		t.Errorf("the kill dialog = %q, want it to say y finds the row", m.confirm.label)
	}
	m.archivedNotice("alpha")
	if !strings.Contains(m.errBar.text, "y finds it") {
		t.Errorf("the silent-kill notice = %q, want it to say y finds the row", m.errBar.text)
	}
}

// Unbound, which is the default, a badge names the palette route rather than
// a key nobody holds -- and with the palette unbound too, its label alone.
func TestAnUnboundWayOutNamesQuickActionsOrNothing(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 40
	m.showArchived = true
	rail := ansi.Strip(railLinesText(m.railLines(36, m.listBodyHeight())))
	if !strings.Contains(rail, ": archived view back to active") {
		t.Errorf("the default badge does not name the quick actions route:\n%s", rail)
	}
	if label := m.archiveFinder(); label != "the archived view" {
		t.Errorf("an unbound archived view reads as %q in a sentence", label)
	}

	m.keys, _ = m.km().Rebind(keymap.ContextList, keymap.QuickActions, nil)
	rail = ansi.Strip(railLinesText(m.railLines(36, m.listBodyHeight())))
	if !strings.Contains(rail, "ARCHIVED   back to active") {
		t.Errorf("with nothing to press, the badge should be its label alone:\n%s", rail)
	}
}

// The undo had a hint and no binding: U was compared against the action
// name, so it never fired. As an action it answers U by default, and a
// rebind moves the key and the hint together.
func TestUndoArchiveAnswersItsKey(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	createSession(t, m, "beta", dir, "")
	m.archiveConfirm = archiveConfirmNever
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !strings.Contains(m.errBar.text, "U undoes it") {
		t.Errorf("notice %q does not name U", m.errBar.text)
	}
	if _, cmd := m.handleKey(runeKey("U")); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if archived(t, m, "alpha") {
		t.Fatalf("U did not bring alpha back; bar says %q", m.errBar.text)
	}

	m.keys, _ = m.km().Rebind(keymap.ContextList, keymap.UndoArchive, []string{"Q"})
	m.selectSessionRow(t, "beta")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !strings.Contains(m.errBar.text, "Q undoes it") || strings.Contains(m.errBar.text, "U undoes") {
		t.Errorf("notice %q does not follow the rebind to Q", m.errBar.text)
	}
}

// The focused frame's title, the dialogs and the guide read the map too.
func TestOtherScreensFollowTheirRebinds(t *testing.T) {
	m := buildModel(t)
	m.keys, _ = m.km().Rebind(keymap.ContextFocus, keymap.Leave, []string{"ctrl+o"})
	if tail := m.focusRuleTail(80, false); !strings.Contains(tail, "ctrl+o back") {
		t.Errorf("focused title = %q, want ctrl+o", tail)
	}
	m.keys, _ = m.km().Rebind(keymap.ContextConfirm, keymap.Confirm, []string{"o", "enter"})
	m.confirm = confirmTarget{action: actionArchive, label: "kill alpha?"}
	if dialog := ansi.Strip(m.viewConfirm()); !strings.Contains(dialog, "o/↵ kill") {
		t.Errorf("the confirm hint does not name o/↵:\n%s", dialog)
	}
	m.keys, _ = m.km().Rebind(keymap.ContextNameSweep, keymap.Confirm, []string{"s"})
	m.nameSweep.plan.Targets = make([]namesweep.Verdict, 1)
	if !hintHas(m.nameSweepHint(), "s") {
		t.Errorf("the sweep hint = %v, want s", m.nameSweepHint())
	}
	m.keys, _ = m.km().Rebind(keymap.ContextList, keymap.Triage, []string{"I"})
	if !welcomeRowHas(m.welcomeFiveKeys(), "I") {
		t.Errorf("the guide's five keys = %v, want I for triage", m.welcomeFiveKeys().rows)
	}
}

func hintHas(hint [][2]string, key string) bool {
	for _, pair := range hint {
		if pair[0] == key {
			return true
		}
	}
	return false
}

func welcomeRowHas(section welcomeSection, key string) bool { return hintHas(section.rows, key) }

// hintLiteral is a key written as a literal where a legend, badge or notice
// shows it. Each entry is a screen whose handler reads that literal itself, so
// the hint is the binding; anything else has to come from the key map.
var hintLiteralAllowed = map[string]string{
	"help.go":       "the key map screen's own keys are read literally in handleHelpKey",
	"launchhint.go": "the setup card's i and c are read literally in its handler",
	"jevkey.go":     "the JEV key panel's x is read literally in its handler",
	"modals.go":     "settings and form keys (J/K in the CLI list) are read literally",
	"welcome.go":    "n on the card is read literally in handleWelcomeKey, w on the key map in handleHelpKey",
	"confirm.go":    "n/esc on a dialog are read literally in handleConfirmAnswer",
}

// formKeys are the keys that make a form work rather than shortcuts over it:
// the keymap package keeps them out of the map, so a hint may name them.
var formKeys = map[string]bool{
	"↵": true, "esc": true, "↑": true, "↓": true, "←": true, "→": true, "↑↓": true, "←→": true,
	"tab": true, "space": true, "type": true, "any key": true, "pgup": true, "pgdn": true,
	"home": true, "end": true, "bksp": true, "ctrl+c": true, "ctrl+v": true, "ctrl+s": true,
}

// A key hint written as a literal is a hint a rebind cannot reach. This walks
// the package's source for the shapes a hint takes -- a keyCap or badge call,
// a [2]string legend pair -- and fails on a literal key that some screen's
// map binds, or any single character, outside the screens whose handlers read
// that literal themselves: "t" was a key no screen bound at all.
func TestNoKeyHintIsWrittenAsALiteral(t *testing.T) {
	bindable := map[string]bool{}
	for _, binding := range keymap.Catalog {
		for _, key := range binding.Keys {
			bindable[keymap.Display(key)] = true
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		if _, ok := hintLiteralAllowed[path]; ok {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		check := func(lit ast.Expr) {
			basic, ok := lit.(*ast.BasicLit)
			if !ok || basic.Kind != token.STRING {
				return
			}
			value, err := strconv.Unquote(basic.Value)
			if err != nil {
				return
			}
			for _, part := range strings.Split(value, "/") {
				part = strings.TrimSpace(part)
				if !formKeys[part] && (bindable[part] || len([]rune(part)) == 1) {
					t.Errorf("%s: key hint %q is a literal; read it from the key map (m.cap, m.hintKey)",
						fset.Position(basic.Pos()), value)
					return
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CallExpr:
				if name, ok := n.Fun.(*ast.Ident); ok && len(n.Args) > 0 {
					switch name.Name {
					case "keyCap", "keyCapQuiet", "keyPill":
						check(n.Args[0])
					case "badge":
						if len(n.Args) > 1 {
							check(n.Args[1])
						}
					}
				}
			case *ast.CompositeLit:
				if arr, ok := n.Type.(*ast.ArrayType); ok && len(n.Elts) == 2 {
					if ident, ok := arr.Elt.(*ast.Ident); ok && ident.Name == "string" {
						check(n.Elts[0])
					}
				}
				// The elements of a [][2]string have their type elided.
				if n.Type == nil && len(n.Elts) == 2 {
					check(n.Elts[0])
				}
			}
			return true
		})
	}
}

// Prose names keys too: "t finds it", "V revives". A single letter a screen
// binds, followed by one of the verbs a hint uses, is a key in a sentence.
func TestNoSentenceNamesABindableLetter(t *testing.T) {
	letters := map[string]bool{}
	for _, binding := range keymap.Catalog {
		for _, key := range binding.Keys {
			if len(key) == 1 && key != " " {
				letters[regexp.QuoteMeta(key)] = true
			}
		}
	}
	var alternatives []string
	for letter := range letters {
		alternatives = append(alternatives, letter)
	}
	sentence := regexp.MustCompile(`(^|[\s(·,;-])(` + strings.Join(alternatives, "|") +
		`) (finds|restores|undoes|revives|brings|reopens|opens|closes|hops|steps|toggles|to (go|restore|open|close|switch|leave|undo))\b`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		if _, ok := hintLiteralAllowed[path]; ok {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, path, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			basic, ok := node.(*ast.BasicLit)
			if !ok || basic.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(basic.Value)
			if err == nil && sentence.MatchString(value) {
				t.Errorf("%s: %q names a key in a sentence; read it from the key map (m.keyOr, m.pressTo)",
					fset.Position(basic.Pos()), value)
			}
			return true
		})
	}
}
