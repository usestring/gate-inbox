package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/snippets"
	"github.com/usestring/gate-inbox/internal/status"
)

func menuKey(letter rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: letter, Text: string(letter)}
}

// menuTrigger is the hotkey menu leader in a focused session.
func menuTrigger() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl}
}

// writeSnippets replaces the model's snippet file and reloads it, so a test
// binds the keys it means rather than whichever defaults shipped.
func writeSnippets(t testing.TB, m *Model, snips []snippets.Snippet) {
	t.Helper()
	encoded, err := json.MarshalIndent(snips, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snippets.Path(m.configDir()), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	m.snips, m.snipErr = snippets.Set{}, ""
	m.loadSnippets()
	if len(m.snips.Snippets) != len(snips) {
		t.Fatalf("loaded %d of %d snippets: %v", len(m.snips.Snippets), len(snips), m.snips.Problems)
	}
}

// The whole feature, from inside a focused pane: the leader opens the menu,
// one bare key, and the operator's own sentence is in the pane, submitted.
func TestMenuKeySendsIntoTheFocusedPane(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Label: "deploy", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, _ := m.handleFocusKey(menuTrigger())
	m = updated.(*Model)
	if !m.quick.active || !m.quick.fromFocus {
		t.Fatalf("the leader did not open the focused menu: active=%v fromFocus=%v", m.quick.active, m.quick.fromFocus)
	}
	updated, _ = m.handleFocusKey(menuKey('d'))
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("the snippet left the session, mode %v: %s", m.mode, m.errBar.text)
	}
	waitForPaneText(t, m, sess.ID, "ship it now")
}

// The same menu from the list, so a queue is answered without entering each
// session first -- and only the row under the cursor is answered.
func TestMenuKeyFromTheListSendsToTheCursorRow(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "other": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	sess := sessionNamed(t, m, "ask")

	m.openQuickMode()
	updated, cmd := m.handleKey(menuKey('d'))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeList {
		t.Fatalf("the snippet changed mode to %v", m.mode)
	}
	waitForPaneText(t, m, sess.ID, "ship it now")

	other := sessionNamed(t, m, "other")
	pane, err := m.tmux.CapturePane(other.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if strings.Contains(squashSpace(ansi.Strip(pane)), squashSpace("ship it now")) {
		t.Fatalf("the snippet also answered %q:\n%s", other.Name, pane)
	}
}

// A lettered snippet's direct chord sends it in one press from the list, with
// no menu opened.
func TestDirectChordSendsFromTheList(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Chord: "alt+shift+d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt | tea.ModShift})
	m = updated.(*Model)
	if m.quick.active {
		t.Fatal("the direct chord opened the hotkey menu")
	}
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	waitForPaneText(t, m, sess.ID, "ship it now")
}

// The same chord answers a focused session without leaving the pane.
func TestDirectChordSendsFromAFocusedSession(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Chord: "alt+shift+d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, cmd := m.handleFocusKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt | tea.ModShift})
	m = updated.(*Model)
	if m.quick.active {
		t.Fatal("the direct chord opened the hotkey menu")
	}
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	waitForPaneText(t, m, sess.ID, "ship it now")
}

// The chord reads shift either as a modifier or folded into the key's
// uppercase code, and neither a bare letter nor plain option+letter names a
// snippet.
func TestDirectChordReadsShiftFromModifierOrCode(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Chord: "option+shift+d", Label: "deploy", Text: "ship it now"}})

	if snip, ok := m.snippetChordFor(tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt | tea.ModShift}); !ok || snip.Key != "d" {
		t.Fatalf("option+shift+d did not name d: %v %v", snip, ok)
	}
	if snip, ok := m.snippetChordFor(tea.KeyPressMsg{Code: 'D', Mod: tea.ModAlt}); !ok || snip.Key != "d" {
		t.Fatalf("shift folded into the code did not name d: %v %v", snip, ok)
	}
	for _, msg := range []tea.KeyPressMsg{
		{Code: 'd', Mod: tea.ModAlt},
		{Code: 'd', Text: "d"},
	} {
		if snip, ok := m.snippetChordFor(msg); ok {
			t.Fatalf("%q named the snippet %q outside its chord", msg.String(), snip.Key)
		}
	}
}

// Shift folded into the code names the shifted chord only: chords compare
// case-folded, so offering option+E as-is would fire an option+e snippet.
// (Not d: focus binds alt+d, so it could never name a snippet.)
func TestFoldedShiftPrefersTheShiftedChord(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{
		{Key: "a", Chord: "alt+e", Text: "plain"},
		{Key: "b", Chord: "alt+shift+e", Text: "shifted"},
	})
	if snip, ok := m.snippetChordFor(tea.KeyPressMsg{Code: 'E', Mod: tea.ModAlt}); !ok || snip.Key != "b" {
		t.Fatalf("option+shift+E folded into the code named %v %v, want b", snip, ok)
	}
	if snip, ok := m.snippetChordFor(tea.KeyPressMsg{Code: 'e', Mod: tea.ModAlt}); !ok || snip.Key != "a" {
		t.Fatalf("option+e named %v %v, want a", snip, ok)
	}
}

// The fold is read for any uppercase letter, not only A-Z: a chord may bind
// any single character.
func TestFoldedShiftReadsNonASCIIUppercase(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{
		{Key: "a", Chord: "alt+é", Text: "plain"},
		{Key: "b", Chord: "alt+shift+é", Text: "shifted"},
	})
	if snip, ok := m.snippetChordFor(tea.KeyPressMsg{Code: 'É', Mod: tea.ModAlt}); !ok || snip.Key != "b" {
		t.Fatalf("option+shift+É folded into the code named %v %v, want b", snip, ok)
	}
}

// A symbol chord fires whether the terminal sends the symbol (option+!) or
// reports shift and the base key apart (option+shift+1, shifted to !).
func TestASymbolChordFiresEitherWayShiftArrives(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "a", Chord: "alt+!", Text: "bang"}})
	for _, msg := range []tea.KeyPressMsg{
		{Code: '!', Mod: tea.ModAlt},
		{Code: '1', ShiftedCode: '!', Mod: tea.ModAlt | tea.ModShift},
	} {
		if snip, ok := m.snippetChordFor(msg); !ok || snip.Key != "a" {
			t.Fatalf("%s named %v %v, want a", msg.String(), snip, ok)
		}
	}
}

// A chord the manager's own map binds stays the manager's on both screens, so
// a snippet file cannot make a documented key behave differently by screen.
func TestManagerBindingOutranksASnippetChord(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{
		{Key: "p", Chord: "ctrl+p", Text: "list snippet"},
		{Key: "c", Chord: "alt+,", Text: "focus snippet"},
	})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = updated.(*Model)
	if m.mode != modeQuickActions {
		t.Fatalf("ctrl+p left the board in %v, want quick actions over the snippet", m.mode)
	}
	pressKey(t, m, key("esc"))

	m.enterFocusOn(t, "ask")
	before := m.chrome
	updated, _ = m.handleFocusKey(tea.KeyPressMsg{Code: ',', Mod: tea.ModAlt})
	m = updated.(*Model)
	if m.chrome == before {
		t.Fatal("alt+, sent the snippet instead of toggling the key hints")
	}
}

// A chord only one screen binds is still the manager's on the other, so the
// same press never sends a snippet on one screen and acts on the other.
func TestAChordEitherScreenBindsNeverNamesASnippet(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{
		{Key: "p", Chord: "ctrl+p", Text: "list binds this"},
		{Key: "q", Chord: "ctrl+q", Text: "focus binds this"},
	})
	for _, msg := range []tea.KeyPressMsg{
		{Code: 'p', Mod: tea.ModCtrl},
		{Code: 'q', Mod: tea.ModCtrl},
	} {
		if snip, ok := m.snippetChordFor(msg); ok {
			t.Errorf("%s named the snippet %q over a manager binding", msg.String(), snip.Key)
		}
	}
}

// A side-sensitive action takes both mirror images of its key, whichever side
// the rail is on, so moving the rail never hands a manager press to a snippet.
func TestAMirroredBindingNeverNamesASnippet(t *testing.T) {
	m := buildModel(t)
	for _, ctx := range []keymap.Context{keymap.ContextList, keymap.ContextFocus} {
		if action, bound := m.km().Action(ctx, "alt+l"); bound {
			t.Fatalf("alt+l is already %s in %v; the test needs it free", action, ctx)
		}
	}
	bindTestKey(t, m, keymap.StepIn, "alt+h")
	writeSnippets(t, m, []snippets.Snippet{{Key: "l", Chord: "alt+l", Text: "mirror of step in"}})
	for _, side := range []string{config.SidebarLeft, config.SidebarRight} {
		m.sidebar = side
		if snip, ok := m.snippetChordFor(tea.KeyPressMsg{Code: 'l', Mod: tea.ModAlt}); ok {
			t.Errorf("rail %s: alt+l named the snippet %q over mirrored step in", side, snip.Key)
		}
	}
}

// A chord the key map took is not advertised as the snippet's: the footer
// falls back to the menu key and the key map says why.
func TestSurfacesDropAChordTheKeyMapTook(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "p", Chord: "ctrl+p", Label: "push", Text: "push it"}})

	if pairs := m.snippetLegend().pairs; len(pairs) != 1 || pairs[0][0] != "p" {
		t.Fatalf("legend pairs = %v, want the menu key p", pairs)
	}
	var help string
	for _, row := range m.snippetHelpSection().rows {
		help += row.key + " " + row.text + "\n"
	}
	if strings.Contains(help, "(menu: p)") {
		t.Errorf("the key map still offers ctrl+p as the snippet's chord:\n%s", help)
	}
	if !strings.Contains(help, "manager key") {
		t.Errorf("the key map does not say why ctrl+p is not the snippet's:\n%s", help)
	}
}

// This is the reason snippets live in the menu. Focused, every key the
// manager does not claim is forwarded to the agent, so the letter on its own
// has to keep reaching the pane -- otherwise a snippet on "d" would eat that
// letter out of everything the operator types.
func TestPlainLetterStillReachesTheFocusedAgent(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	for _, r := range "ddd" {
		updated, cmd := m.handleFocusKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(*Model)
		if cmd != nil {
			m.applyCmd(t, cmd)
		}
	}
	waitForPaneText(t, m, sess.ID, "ddd")

	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if strings.Contains(squashSpace(ansi.Strip(pane)), squashSpace("ship it now")) {
		t.Fatalf("typing d fired the snippet on d:\n%s", pane)
	}
}

// An unbound menu key must not be swallowed either way: it is not a snippet,
// and the list has no other meaning for it.
func TestUnboundMenuKeyDoesNothing(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	m.openQuickMode()
	updated, _ := m.handleKey(menuKey('k'))
	m = updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("an unbound menu key said %q", m.errBar.text)
	}
	if !m.quick.active {
		t.Fatal("an unbound menu key closed the menu")
	}
}

// The old chord is dead: outside the menu it reaches nothing, and inside the
// menu it is a different key from the snippet's bare letter.
func TestOldChordSendsNothing(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	sess := sessionNamed(t, m, "ask")

	old := tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl | tea.ModAlt}
	updated, _ := m.handleKey(old)
	m = updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("the old chord said %q", m.errBar.text)
	}

	m.openQuickMode()
	updated, _ = m.handleKey(old)
	m = updated.(*Model)
	if !m.quick.active {
		t.Fatal("the old chord closed the menu")
	}
	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if strings.Contains(squashSpace(ansi.Strip(pane)), squashSpace("ship it now")) {
		t.Fatalf("the old chord fired the snippet:\n%s", pane)
	}
}

// Same guards as ±, because it is the same path: a shell would run the
// sentence as a command.
func TestSnippetRefusesAShell(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	m.applyCmd(t, m.refreshCmd())
	sess := spawnTerminal(t, m)
	m.selectSessionRow(t, sess.Name)

	snip, ok := m.menuSnippetFor("d")
	if !ok {
		t.Fatal("d did not resolve")
	}
	if _, _ = m.sendSnippetToSelected(snip); m.errBar.text != shellPromptHint(sess.Name) {
		t.Fatalf("err = %q, want the shell refusal", m.errBar.text)
	}
}

// A group has no pane. Silence would read as a broken key.
func TestSnippetOnAGroupSaysWhatToSelect(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	groupAt(t, m, "work", t.TempDir())

	snip, _ := m.menuSnippetFor("d")
	if _, _ = m.sendSnippetToSelected(snip); m.errBar.text == "" {
		t.Fatal("a snippet on a group said nothing")
	}
}

// An answered session is one the operator is waiting on again.
func TestSnippetClearsTheAckedFlag(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	if err := m.store.SetAcked(sess.ID, true); err != nil {
		t.Fatalf("set acked: %v", err)
	}
	m.selectSessionRow(t, "ask")

	snip, _ := m.menuSnippetFor("d")
	if _, _ = m.sendSnippetToSelected(snip); m.errBar.text == "" {
		t.Fatal("a send says so on the error bar")
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Acked {
		t.Fatal("the snippet left the acked flag set")
	}
}

// The leader opens the menu from the list too, under the same key as in a
// focused pane, and the menu answers the row under the cursor.
func TestLeaderOpensTheMenuFromTheList(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, _ := m.handleKey(menuTrigger())
	m = updated.(*Model)
	if !m.quick.active || m.quick.fromFocus {
		t.Fatalf("the leader did not open the list menu: active=%v fromFocus=%v", m.quick.active, m.quick.fromFocus)
	}
	updated, cmd := m.handleKey(menuKey('d'))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	waitForPaneText(t, m, sess.ID, "ship it now")
}

// The § snippet from inside a focused pane: the leader opens the menu and
// the bare § answers the pane, leaving the operator where they were.
func TestSectionSnippetSendsIntoTheFocusedPane(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: snippets.SectionKey, Label: "progress", Text: "summarise it"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, _ := m.handleFocusKey(menuTrigger())
	m = updated.(*Model)
	updated, _ = m.handleFocusKey(menuKey('§'))
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("the § menu key left the session, mode %v: %s", m.mode, m.errBar.text)
	}
	waitForPaneText(t, m, sess.ID, "summarise it")
}

// A bare § outside the menu is the handover key and must stay one. The
// snippet sits on the same physical key and answers only in the menu: if the
// bare press also reached the snippet, handing a session over would answer it
// on the way out.
func TestBareSectionStillHandsOverAndSendsNothing(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: snippets.SectionKey, Text: "summarise it"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, cmd := m.handleFocusKey(tea.KeyPressMsg{Code: '§', Text: "§"})
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode == modeFocus {
		t.Fatal("a bare § did not hand the session over")
	}
	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if strings.Contains(squashSpace(ansi.Strip(pane)), squashSpace("summarise it")) {
		t.Fatalf("a bare § outside the menu fired the snippet:\n%s", pane)
	}
}

// From the list the § menu key answers the row under the cursor, like every
// snippet.
func TestSectionSnippetFromTheListSendsToTheCursorRow(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: snippets.SectionKey, Text: "summarise it"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	sess := sessionNamed(t, m, "ask")

	m.openQuickMode()
	updated, cmd := m.handleKey(menuKey('§'))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeList {
		t.Fatalf("§ changed mode to %v", m.mode)
	}
	waitForPaneText(t, m, sess.ID, "summarise it")
}

// The leader toggles the focused menu back off: there is no typing it, so
// the same key is the way out besides esc.
func TestLeaderTogglesTheFocusedMenuClosed(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")

	updated, _ := m.handleFocusKey(menuTrigger())
	m = updated.(*Model)
	if !m.quick.active {
		t.Fatal("the leader did not open the menu")
	}
	updated, _ = m.handleFocusKey(menuTrigger())
	m = updated.(*Model)
	if m.quick.active {
		t.Fatal("the leader did not close the menu it opened")
	}
	if m.mode != modeFocus {
		t.Fatalf("closing the menu left the session, mode %v", m.mode)
	}
}

// The focused menu paints on screen: the snippets and how to leave.
func TestFocusedMenuRendersInTheFrame(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Label: "deploy", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	m.width, m.height = 200, 60

	updated, _ := m.handleFocusKey(menuTrigger())
	m = updated.(*Model)
	frame := ansi.Strip(m.frame())
	for _, want := range []string{"Hotkeys", "d", "deploy", "send snippet", "close"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the focused frame is missing %q:\n%s", want, frame)
		}
	}
}

// The arrows that retarget the menu on the list must not move the cursor
// under a focused pane: the menu answers the pane on screen.
func TestFocusedMenuArrowsDoNotMoveTheCursor(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "other": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")

	updated, _ := m.handleFocusKey(menuTrigger())
	m = updated.(*Model)
	start := m.cursor
	for _, code := range []rune{tea.KeyUp, tea.KeyDown} {
		updated, _ = m.handleFocusKey(tea.KeyPressMsg{Code: code})
		m = updated.(*Model)
	}
	if m.cursor != start {
		t.Fatalf("arrows in the focused menu moved the cursor to %d", m.cursor)
	}
	if !m.quick.active {
		t.Fatal("arrows closed the focused menu")
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("the menu moved focus to %q", got)
	}
}

/* ------------------------------------------------------------------- surfaces */

// The key map is the viewer: it names the menu opener, lists the bare keys
// it takes, and says which file to edit, which is the whole interface for
// adding one.
func TestHelpListsSnippetsAndTheirFile(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Label: "deploy", Text: "ship it now"}})

	section := m.snippetHelpSection()
	var flat string
	for _, row := range section.rows {
		flat += row.key + " " + row.text + "\n"
	}
	for _, want := range []string{"hotkey menu", "d", "ship it now", snippets.Path(m.configDir())} {
		if !strings.Contains(flat, want) {
			t.Errorf("the key map does not mention %q:\n%s", want, flat)
		}
	}
	// And it is reachable from the catalog the screen actually renders.
	found := false
	for _, s := range m.helpCatalog() {
		if s.title == "snippets" {
			found = true
		}
	}
	if !found {
		t.Fatal("the snippets tier is not in the rendered key map")
	}
}

// A refused entry is otherwise a key that silently does nothing, with the
// explanation nowhere a user can reach.
func TestHelpExplainsARefusedEntry(t *testing.T) {
	m := buildModel(t)
	if err := os.WriteFile(snippets.Path(m.configDir()),
		[]byte(`[{"key":"d","text":"ship it"},{"key":"1","text":"never fires"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.snips, m.snipErr = snippets.Set{}, ""
	m.loadSnippets()

	var flat string
	for _, row := range m.snippetHelpSection().rows {
		flat += row.text + "\n"
	}
	if !strings.Contains(flat, "single letter") {
		t.Fatalf("the key map does not explain why 1 was refused:\n%s", flat)
	}
}

func TestFooterAdvertisesSnippets(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Chord: "alt+shift+d", Label: "deploy", Text: "ship it now"}})

	section := m.snippetLegend()
	if len(section.pairs) != 1 || section.pairs[0][0] != keymap.Display("alt+shift+d") || section.pairs[0][1] != "deploy" {
		t.Fatalf("legend pairs = %v", section.pairs)
	}
	if !section.quiet {
		t.Fatal("the snippets tier must be quiet: it recedes behind the row's own keys")
	}
	createSession(t, m, "footer-snippet", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())
	m.width = 100
	footer := ansi.Strip(m.viewFooter())
	if !strings.Contains(footer, "Snippets") || !strings.Contains(footer, "deploy") {
		t.Fatalf("list footer does not advertise snippets:\n%s", footer)
	}
}

// A lettered snippet's every surface prints its direct chord; § has no chord
// and stays bare, the menu key it answers to.
func TestSurfacesPrintTheDirectChord(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{
		{Key: "d", Chord: "alt+shift+d", Label: "deploy", Text: "ship it now"},
		{Key: snippets.SectionKey, Label: "progress", Text: "summarise it"},
	})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	var legend []string
	for _, pair := range m.snippetLegend().pairs {
		legend = append(legend, pair[0])
	}
	var help string
	for _, row := range m.snippetHelpSection().rows {
		help += row.key + " " + row.text + "\n"
	}
	surfaces := map[string]string{
		"footer":  strings.Join(legend, " "),
		"key map": help,
	}
	for name, got := range surfaces {
		if !strings.Contains(got, keymap.Display("alt+shift+d")) {
			t.Errorf("the %s does not print d's direct chord: %q", name, got)
		}
		if !strings.Contains(got, snippets.SectionKey) {
			t.Errorf("the %s does not print the %s snippet: %q", name, snippets.SectionKey, got)
		}
		for _, never := range []string{"ctrl+alt", "alt+" + snippets.SectionKey} {
			if strings.Contains(got, never) {
				t.Errorf("the %s still names the old chord %q: %q", name, never, got)
			}
		}
	}
}

// With no snippets nothing is advertised anywhere -- an operator who never
// wrote the file sees exactly the manager they had before.
func TestNoSnippetsAddsNothingToTheFooter(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{})

	if pairs := m.snippetLegend().pairs; len(pairs) != 0 {
		t.Fatalf("footer offered %v with no snippets defined", pairs)
	}
	createSession(t, m, "footer-empty", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())
	m.width = 100
	if footer := ansi.Strip(m.viewFooter()); strings.Contains(footer, "Snippets") {
		t.Fatalf("list footer advertises a snippets tier with none defined:\n%s", footer)
	}
}

/* ---------------------------------------------------------------------- loading */

// First run writes the file, so the feature is discoverable without reading
// documentation for a filename.
func TestFirstRunWritesTheSnippetsFile(t *testing.T) {
	m := buildModel(t)
	if _, err := os.Stat(snippets.Path(m.configDir())); err != nil {
		t.Fatalf("snippets.json was not written on first run: %v", err)
	}
	if len(m.snips.Snippets) == 0 {
		t.Fatal("no snippets loaded on first run")
	}
}

// A broken file costs its own feature and nothing else, and says so.
func TestUnreadableFileIsReportedNotFatal(t *testing.T) {
	m := buildModel(t)
	if err := os.WriteFile(snippets.Path(m.configDir()), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.snips, m.snipErr = snippets.Set{}, ""
	m.loadSnippets()

	if m.snipErr == "" {
		t.Fatal("a broken snippets.json was not reported")
	}
	if len(m.snips.Snippets) != 0 {
		t.Fatal("a broken file still bound keys")
	}
	var flat string
	for _, row := range m.snippetHelpSection().rows {
		flat += row.text + "\n"
	}
	if !strings.Contains(flat, "could not be read") {
		t.Fatalf("the key map does not surface the read failure:\n%s", flat)
	}
}

// The directory is derived from the hook manager's rather than resolved from
// the environment a second time, which a session-scoped command may not share.
func TestConfigDirIsTheHooksParent(t *testing.T) {
	m := buildModel(t)
	if got, want := m.configDir(), filepath.Dir(m.hooks.Dir()); got != want {
		t.Fatalf("configDir() = %q, want %q", got, want)
	}
}
