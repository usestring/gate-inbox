package ui

import (
	"encoding/json"
	"github.com/usestring/gate-inbox/internal/keymap"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/snippets"
	"github.com/usestring/gate-inbox/internal/status"
)

func chordMsg(letter rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: letter, Mod: tea.ModCtrl | tea.ModAlt}
}

// sectionMsg is alt+§, the one snippet key off the chord.
func sectionMsg() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: '§', Mod: tea.ModAlt}
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

// The whole feature, from inside a session: one chord and the operator's own
// sentence is in the pane, submitted.
func TestSnippetKeySendsIntoTheFocusedPane(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Label: "deploy", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, _ := m.handleFocusKey(chordMsg('d'))
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("the snippet left the session, mode %v: %s", m.mode, m.errBar.text)
	}
	waitForPaneText(t, m, sess.ID, "ship it now")
}

// The same chord from the list, so a queue is answered without entering each
// session first -- and only the row under the cursor is answered.
func TestSnippetKeyFromTheListSendsToTheCursorRow(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "other": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, cmd := m.handleKey(chordMsg('d'))
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

// This is the reason the bindings are confined to one chord. Focused, every
// key the manager does not claim is forwarded to the agent, so the letter on
// its own has to keep reaching the pane -- otherwise a snippet on "d" would
// eat that letter out of everything the operator types.
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
		t.Fatalf("typing d fired the snippet bound to ctrl+alt+d:\n%s", pane)
	}
}

// An unbound chord must not be swallowed either way: it is not a snippet, and
// the list has no other meaning for it.
func TestUnboundChordDoesNothing(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	updated, _ := m.handleKey(chordMsg('k'))
	m = updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("an unbound chord said %q", m.errBar.text)
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

	snip, ok := m.snippetFor("ctrl+alt+d")
	if !ok {
		t.Fatal("ctrl+alt+d did not resolve")
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

	snip, _ := m.snippetFor("ctrl+alt+d")
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

	snip, _ := m.snippetFor("ctrl+alt+d")
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

// The quick bar is a place a session is on screen, so the chord acts there
// too -- and it leaves the half-written prompt alone, because sending a
// snippet is not abandoning what is being composed.
func TestSnippetFromTheQuickBarKeepsTheTypedPrompt(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	sess := sessionNamed(t, m, "ask")

	m.openQuickMode()
	m.quick.input.SetValue("half written")

	updated, cmd := m.handleKey(chordMsg('d'))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := m.quick.input.Value(); got != "half written" {
		t.Fatalf("the quick prompt now reads %q, want the typed text untouched", got)
	}
	waitForPaneText(t, m, sess.ID, "ship it now")
}

// The § snippet from inside a session: alt+§ answers the pane and leaves the
// operator where they were, exactly as a chord snippet does.
func TestSectionSnippetSendsIntoTheFocusedPane(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: snippets.SectionKey, Label: "progress", Text: "summarise it"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, _ := m.handleFocusKey(sectionMsg())
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("alt+§ left the session, mode %v: %s", m.mode, m.errBar.text)
	}
	waitForPaneText(t, m, sess.ID, "summarise it")
}

// A bare § is the handover key and must stay one. The snippet sits on the same
// physical key and the alt is all that separates them: if the bare press also
// reached the snippet, handing a session over would answer it on the way out.
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
		t.Fatalf("a bare § fired the snippet bound to alt+§:\n%s", pane)
	}
}

// From the list it answers the row under the cursor, like every snippet.
func TestSectionSnippetFromTheListSendsToTheCursorRow(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: snippets.SectionKey, Text: "summarise it"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, cmd := m.handleKey(sectionMsg())
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeList {
		t.Fatalf("alt+§ changed mode to %v", m.mode)
	}
	waitForPaneText(t, m, sess.ID, "summarise it")
}

/* ------------------------------------------------------------------- surfaces */

// The key map is the viewer: it lists what is bound and says which file to
// edit, which is the whole interface for adding one.
func TestHelpListsSnippetsAndTheirFile(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Label: "deploy", Text: "ship it now"}})

	section := m.snippetHelpSection()
	var flat string
	for _, row := range section.rows {
		flat += row.key + " " + row.text + "\n"
	}
	for _, want := range []string{"^" + keymap.Display("alt+d"), "ship it now", keymap.Display("ctrl+alt+"), snippets.Path(m.configDir())} {
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
		[]byte(`[{"key":"d","text":"ship it"},{"key":"m","text":"never fires"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.snips, m.snipErr = snippets.Set{}, ""
	m.loadSnippets()

	var flat string
	for _, row := range m.snippetHelpSection().rows {
		flat += row.text + "\n"
	}
	if !strings.Contains(flat, "alt+enter") {
		t.Fatalf("the key map does not explain why ctrl+alt+m was refused:\n%s", flat)
	}
}

func TestFooterAdvertisesSnippets(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Label: "deploy", Text: "ship it now"}})

	section := m.snippetLegend()
	if len(section.pairs) != 1 || section.pairs[0][0] != "^"+keymap.Display("alt+d") || section.pairs[0][1] != "deploy" {
		t.Fatalf("legend pairs = %v", section.pairs)
	}
	if !section.quiet {
		t.Fatal("the snippets tier must be quiet: it recedes behind the row's own keys")
	}
}

// Every surface prints the § binding as alt+§. The ^alt+ compression would
// claim a ctrl it does not have, and an operator reading it would press a key
// that never arrives.
func TestSurfacesSpellTheSectionKeyAsAltAlone(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{
		{Key: "d", Label: "deploy", Text: "ship it now"},
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
		"footer":    strings.Join(legend, " "),
		"key map":   help,
		"quick bar": ansi.Strip(m.quickSnippetLine(120)),
	}
	section, chord := keymap.Display("alt+§"), "^"+keymap.Display("alt+d")
	for name, got := range surfaces {
		if !strings.Contains(got, section) || strings.Contains(got, "^"+section) {
			t.Errorf("the %s does not print the § snippet as %s: %q", name, section, got)
		}
		if !strings.Contains(got, chord) {
			t.Errorf("the %s lost the chord spelling for the letters: %q", name, got)
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
	if rows := m.snippetQuickRows(); len(rows) != 0 {
		t.Fatalf("the quick bar offered %v with no snippets defined", rows)
	}
}

// The quick bar lists them so the operator sees the message is already on a
// key before typing it out.
func TestQuickBarListsSnippetsForASession(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Label: "deploy", Text: "ship it now"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	line := ansi.Strip(m.quickSnippetLine(120))
	if !strings.Contains(line, "^"+keymap.Display("alt+d")) || !strings.Contains(line, "deploy") {
		t.Fatalf("the quick bar does not list the snippet: %q", line)
	}
}

// On a group the bar spawns rather than answers, so a snippet has no pane to
// reach: listing them there would offer keys that refuse.
func TestQuickBarListsNothingOnAGroup(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Text: "ship it now"}})
	groupAt(t, m, "work", t.TempDir())

	if line := m.quickSnippetLine(120); line != "" {
		t.Fatalf("the quick bar offered snippets on a group row: %q", line)
	}
}

// The bar's height is measured from what it renders, so the suggestion line
// must never wrap: a long set would push the live pane down by however many
// snippets the operator happened to define.
func TestQuickBarSuggestionStaysOneLine(t *testing.T) {
	m := buildModel(t)
	var many []snippets.Snippet
	for _, key := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		many = append(many, snippets.Snippet{Key: key, Label: strings.Repeat(key, 30), Text: "x"})
	}
	writeSnippets(t, m, many)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	line := m.quickSnippetLine(80)
	if strings.Contains(line, "\n") {
		t.Fatalf("the suggestion line wrapped:\n%s", line)
	}
	if got := cellWidth(ansi.Strip(line)); got > 80 {
		t.Fatalf("suggestion line is %d cells wide, want at most 80", got)
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
