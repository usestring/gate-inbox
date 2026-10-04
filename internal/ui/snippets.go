package ui

// Snippets are the operator's own canned answers on their own keys.
// internal/snippets owns the file and what may bind; this
// file is the manager's side of it -- when the set is read, which keys reach
// it, and where the bindings are advertised.
//
// They fire from the hotkey menu, opened from the list and from inside a
// focused session, because those are the two places a session is the thing in
// front of you. A form and the settings screen are not: nothing there is a
// session, and a key that acted on some off-screen row would be a key that
// answers an agent you are not looking at. The one exception is the bare ±
// key, which fires anywhere a session is in front of you: it is a character
// no agent CLI wants, so taking it costs the pane nothing.
//
// Advertised on the list footer, in the key map and in the hotkey menu.
// The focused footer leaves them to the key map: its row is already full, and
// a tier of its own would move the box and resize every session's pane. It is the same trade the § exit
// makes there, resolved the same way: the key map names what the footer has
// no room for.

import (
	"path/filepath"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/snippets"
)

// loadSnippets reads the file into the model. The set changes only at startup
// or when Settings closes, so a file edit cannot change what a key does between
// two presses without the operator returning through that explicit boundary.
//
// A read failure is reported and leaves no snippets rather than falling back
// to the defaults, which would silently bind three keys the operator's own
// file had replaced. Nothing else about the manager depends on this, so a
// broken snippets.json costs its own feature and nothing more.
func (m *Model) loadSnippets() {
	dir := m.configDir()
	if dir == "" {
		return
	}
	set, err := snippets.Load(dir)
	// Both fields are assigned on every path, so a reload after a failure
	// cannot leave the previous set bound beside an error explaining that
	// nothing is.
	m.snips, m.snipErr = set, ""
	if err != nil {
		m.snipErr = err.Error()
	}
}

// configDir is the directory config.toml and snippets.json share. Derived
// from the hook manager's, which is the config directory plus one segment,
// the way the name sweep already derives it -- rather than resolving it from
// the environment a second time, which a session-scoped command may not share.
func (m *Model) configDir() string {
	if m.hooks == nil {
		return ""
	}
	return filepath.Dir(m.hooks.Dir())
}

func (m *Model) openSnippetEditor() (tea.Model, tea.Cmd) {
	dir := m.configDir()
	if dir == "" {
		m.errBar.text = "snippets file is unavailable"
		return m, nil
	}
	return m.launchEditor(snippets.Path(dir))
}

// snippetFor returns the snippet a keypress names outside the menu, if any.
// Only the bare ± key binds there; every other snippet answers to its bare
// key in the menu alone.
//
// The IsBinding test is not redundant with Get's own comparison: this runs on
// every keypress the manager sees, and it settles the overwhelmingly common
// answer -- an ordinary key, which is not ours -- without walking the set.
func (m *Model) snippetFor(key string) (snippets.Snippet, bool) {
	if !snippets.IsBinding(key) {
		return snippets.Snippet{}, false
	}
	return m.snips.Get(key)
}

// snippetChordFor returns the snippet a direct chord press names, so it can be
// sent in one press without opening the hotkey menu. The file names each
// chord; this only has to spell the press the way the file might, so keyName
// (rebuilt from the key code, what a chord binds under) and the terminal's own
// name are both offered.
//
// A chord either screen's map binds is the manager's on both, not only on the
// screen that binds it: a snippet on ctrl+q would otherwise send from the list
// and leave focus, so the same press would mean two things by screen.
func (m *Model) snippetChordFor(msg tea.KeyMsg) (snippets.Snippet, bool) {
	for _, ctx := range []keymap.Context{keymap.ContextList, keymap.ContextFocus} {
		if _, bound := m.action(ctx, msg); bound {
			return snippets.Snippet{}, false
		}
	}
	for _, name := range chordNames(msg) {
		if snip, ok := m.snips.Chord(name); ok {
			return snip, true
		}
	}
	return snippets.Snippet{}, false
}

// chordNames is the spellings a press might name a chord by. A terminal may
// fold shift into the key's code rather than report the modifier -- option+
// shift+c arrives as option+C -- so an uppercase code also offers the shifted
// spelling -- and only that one, since chords compare case-folded and option+C
// would otherwise also name an option+c snippet. A plain lowercase chord stays
// unambiguous: shift that is nowhere reported cannot be invented.
//
// A symbol goes the other way. Chords name the symbol shift types (option+!,
// never option+shift+1), which is what a legacy terminal sends; a terminal
// that reports shift and the base key separately is folded back to it here.
func chordNames(msg tea.KeyMsg) []string {
	key := msg.Key()
	if r := key.ShiftedCode; key.Mod&tea.ModShift != 0 && r != 0 && !unicode.IsLetter(r) {
		return []string{tea.Key{Code: r, Mod: key.Mod &^ tea.ModShift}.String()}
	}
	if key.Mod&tea.ModShift == 0 {
		for _, r := range []rune{key.ShiftedCode, key.Code} {
			if unicode.IsUpper(r) {
				return []string{tea.Key{Code: unicode.ToLower(r), Mod: key.Mod | tea.ModShift}.String()}
			}
		}
	}
	return []string{keyName(msg), msg.String()}
}

// sendSnippetToSelected answers the row the list cursor is on. A group has no
// pane, and saying so beats a key that looks like it did nothing.
func (m *Model) sendSnippetToSelected(snip snippets.Snippet) (tea.Model, tea.Cmd) {
	entry, ok := m.selectedRow()
	if !ok {
		m.errBar.text = "nothing selected"
		return m, nil
	}
	if entry.isGroup {
		m.errBar.text = "select a session to send " + snip.Quoted() + " to"
		return m, nil
	}
	return m, m.sendSentence(entry.sess, snip.Text, snip.Quoted(), snip.Submits(), false)
}

// sendSnippetToFocused answers the focused session from the hotkey menu: the
// same whole-answer send a snippet is on the list, with the drain's
// auto-proceed, so a submitted snippet hands over exactly as one sent from
// the pane would.
func (m *Model) sendSnippetToFocused(snip snippets.Snippet) (tea.Model, tea.Cmd) {
	sess, ok := m.selected()
	if !ok {
		m.errBar.text = "nothing selected"
		return m, nil
	}
	m.noteFocusActivity()
	return m, m.sendSentence(sess, snip.Text, snip.Quoted(), snip.Submits(), m.autoProceeds())
}

// snippetLegend is the footer's tier for the snippets that exist. Each pair
// carries the snippet's direct chord, so the toolbar shows the one-press keys
// that send without the menu.
//
// It is quiet and it goes last, where a narrow footer drops it first -- the
// same place the row legend puts the priority mark, and for the same reason.
// The keys that act on the session under the cursor are what a three-row
// budget must keep; a snippet is also listed in the hotkey menu, and can
// always be sent by hand.
func (m *Model) snippetLegend() legendSection {
	if len(m.snips.Snippets) == 0 {
		return legendSection{}
	}
	pairs := make([][2]string, 0, len(m.snips.Snippets))
	for _, snip := range m.snips.Snippets {
		pairs = append(pairs, [2]string{snippetCap(snip), snip.Title()})
	}
	return legendSection{title: "Snippets", quiet: true, pairs: pairs}
}

// snippetCap is a snippet's direct chord as every surface prints it, falling
// back to the bare menu key for a snippet that has no chord (§ and ±).
func snippetCap(snip snippets.Snippet) string {
	if snip.Chord != "" {
		return keymap.Display(snip.Chord)
	}
	return keymap.Display(snip.Binding())
}

// snippetHelpSection is the key map's tier for the snippets: how the menu
// opens, the keys that exist in it, where they are written, and every entry
// that would not bind.
//
// This is the in-app viewer. The file is named on screen because editing it is
// the whole interface for now: a key map that lists bare keys without saying
// what writes them leaves the reader with no way to add another. The
// refusals are here for the same reason -- an entry that did not bind is
// otherwise a key that does nothing, with the explanation sitting in a
// process nobody can see.
func (m *Model) snippetHelpSection() helpSection {
	rows := []helpRow{note("the chord sends in one press; the hotkey menu lists the rest.")}
	for _, snip := range m.snips.Snippets {
		verb := "send "
		if !snip.Submits() {
			verb = "type "
		}
		text := verb + snip.Quoted()
		if snip.Chord != "" {
			text += " (menu: " + snip.Key + ")"
		}
		rows = append(rows, lit(snippetCap(snip), text))
	}
	switch {
	case m.snipErr != "":
		rows = append(rows, note("✕ snippets.json could not be read: "+m.snipErr))
	case len(m.snips.Snippets) == 0:
		rows = append(rows, note("none yet — add them to "+snippets.Path(m.configDir())))
	default:
		rows = append(rows, note("edit them in "+snippets.Path(m.configDir())))
	}
	for _, problem := range m.snips.Problems {
		rows = append(rows, note("✕ "+problem))
	}
	return helpSection{title: "snippets", rows: rows}
}
