package ui

// Snippets are the operator's own canned answers on their own keys.
// internal/snippets owns the file and what may bind; this
// file is the manager's side of it -- when the set is read, which keys reach
// it, and where the bindings are advertised.
//
// They fire from the list, from triage and from inside a focused session,
// because those are the three places a session is the thing in front of you.
// A form and the settings screen are not: nothing there is a
// session, and a chord that acted on some off-screen row would be a key that
// answers an agent you are not looking at.
//
// Advertised on the list footer, in the key map, under the quick prompt, and
// on the gate footer's tier, whose tail row has room for them once the
// session controls are named. The ordinary focused footer still leaves them
// to the key map: its row is already full, and a tier of its own would move
// the box and resize every session's pane. It is the same trade the § exit
// makes there, resolved the same way: the key map names what the footer has
// no room for.

import (
	"github.com/usestring/gate-inbox/internal/keymap"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

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

// snippetFor returns the snippet a keypress names, if any.
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
	m.sendSentence(entry.sess, snip.Text, snip.Quoted())
	return m, nil
}

// snippetLegend is the footer's tier for the snippets that exist.
//
// It is quiet and it goes last, where a narrow footer drops it first -- the
// same place the row legend puts the priority mark, and for the same reason.
// The keys that act on the session under the cursor are what a three-row
// budget must keep; a snippet is a shortcut for a message the quick prompt
// can always send by hand.
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

// snippetCap is a snippet's key as every surface prints it.
//
// Every chord entry repeats ctrl, so it compresses to ^ with the OS's own alt
// spelling. The § key is on alt alone and ± on nothing, and compressing either
// the same way would claim a ctrl it does not have -- a key the operator would
// press and never see arrive.
func snippetCap(snip snippets.Snippet) string {
	if snip.Key == snippets.SectionKey || snip.Bare() {
		return keymap.Display(snip.Binding())
	}
	return "^" + keymap.Display("alt+"+snip.Key)
}

// snippetHelpSection is the key map's tier for the snippets: the bindings
// that exist, where they are written, and every entry that would not bind.
//
// This is the in-app viewer. The file is named on screen because editing it is
// the whole interface for now: a key map that lists three chords without
// saying what writes them leaves the reader with no way to add a fourth. The
// refusals are here for the same reason -- an entry that did not bind is
// otherwise a key that does nothing, with the explanation sitting in a
// process nobody can see.
//
// Keys read as ^alt+c rather than ctrl+alt+c, matching the footer, and because
// helpKeyColumn measures the static catalog: the spelled-out chord is wider
// than anything in it and would render clipped against its own description.
// The chord is spelled out once, in the tier's opening row. When § is bound a
// second row says it is on alt alone: a reader who saw only "^alt+ is
// ctrl+alt" would read alt+§ as a typo for it, and it has a row of its own
// because the opening one is already as wide as the card allows.
func (m *Model) snippetHelpSection() helpSection {
	rows := []helpRow{note("^" + keymap.Display("alt+") + " is " + keymap.Display("ctrl+alt+") + ". Sends to the selected or focused session.")}
	section := snippets.SectionChord + snippets.SectionKey
	if _, ok := m.snips.Get(section); ok {
		rows = append(rows, note(keymap.Display(section)+" has no ctrl: ctrl+"+snippets.SectionKey+" never gets through tmux"))
	}
	for _, snip := range m.snips.Snippets {
		rows = append(rows, lit(snippetCap(snip), "send "+snip.Quoted()))
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

// snippetQuickRows are the lines the quick prompt bar lists under its input:
// what you could send with one key instead of typing it.
//
// Only when there are snippets, and never the refusals -- the bar is a place
// to write a message, not to debug a config file, and the key map carries
// those. Empty when nothing is bound, so the bar is exactly what it was.
func (m *Model) snippetQuickRows() []string {
	if len(m.snips.Snippets) == 0 {
		return nil
	}
	rows := make([]string, 0, len(m.snips.Snippets))
	for _, snip := range m.snips.Snippets {
		rows = append(rows, snippetCap(snip)+"  "+snip.Title())
	}
	return rows
}
