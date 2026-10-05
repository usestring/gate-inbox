package ui

// The manager's side of the key map. internal/keymap owns the catalog, the
// file and what may bind; this file is when the map is read, how a press is
// turned into an action, and how a rebind is written back.
//
// Every handler that used to compare msg.String() to a letter now asks which
// action the press stands for on the screen it is answering for. That is the
// whole change from the handlers' side, and it is why a rebind reaches them:
// none of them holds a key any more.

import (
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
)

// defaultKeys is the catalog with nothing overridden. Built once and shared,
// because a model that never read a file -- every test that builds one by
// hand, and a board with no config directory -- still has to answer keys,
// and answering them with the defaults is what those callers expect.
var defaultKeys = sync.OnceValue(func() *keymap.Map {
	resolved, _ := keymap.New(nil)
	return resolved
})

// km is the map this model answers keys with.
func (m *Model) km() *keymap.Map {
	if m.keys == nil {
		return defaultKeys()
	}
	return m.keys
}

// loadKeys reads the operator's key file into the model. Once at startup,
// for the reason snippets are read once: a file re-read between two presses
// of a key would make it mean two different things.
//
// A file that could not be read leaves the defaults in place rather than
// leaving the board with no keys -- the failure mode of a broken snippets
// file is three chords that do nothing, and the failure mode here would be a
// board that cannot be worked at all.
func (m *Model) loadKeys() {
	m.keys, m.keyProblems = defaultKeys(), nil
	extra := m.extraBindings()
	var overrides keymap.Overrides
	if dir := m.configDir(); dir != "" {
		if err := keymap.WriteReferenceIfMissing(dir); err != nil {
			m.keyProblems = append(m.keyProblems, "could not write "+keymap.Path(dir)+": "+err.Error())
		}
		loaded, err := keymap.Load(dir)
		if err != nil {
			m.keyProblems = append(m.keyProblems, keymap.FileName+" could not be read: "+err.Error())
		}
		overrides = loaded
	}
	if overrides == nil && len(extra) == 0 {
		return
	}
	resolved, problems := keymap.NewWithAliases(overrides, extra, m.extraAliases())
	m.keys = resolved
	for _, problem := range problems {
		m.keyProblems = append(m.keyProblems, problem.Error())
	}
}

// keyName is the name a press is matched under. msg.String() answers with
// the text a terminal reported where it has one, and an enhanced keyboard
// protocol reports text for an alt-modified rune -- so alt+. arrives as a
// plain "." there and would be forwarded to the agent instead of dismissing
// the session. Rebuilding the name from the code and the modifiers is what the
// focused handler already did by hand for its three keys; every screen reads
// keys through it now, because any of them can be rebound onto a chord.
func keyName(msg tea.KeyMsg) string {
	key := msg.Key()
	if key.Mod&^tea.ModShift == 0 {
		return msg.String()
	}
	name := tea.Key{Code: key.Code, ShiftedCode: key.ShiftedCode, Mod: key.Mod}
	name.Text = ""
	return name.String()
}

// action is which action a press stands for on one screen.
func (m *Model) action(ctx keymap.Context, msg tea.KeyMsg) (keymap.Action, bool) {
	// Both spellings are offered: the name rebuilt from the key code, which
	// is what a chord binds under, and the one the terminal printed, which
	// is what a plain letter or a named key like § arrives as.
	if action, ok := m.km().Action(ctx, keyName(msg)); ok {
		return action, true
	}
	return m.km().Action(ctx, msg.String())
}

// isAction reports whether a press is one particular action, for a handler
// that already knows the one key it is looking for.
func (m *Model) isAction(ctx keymap.Context, want keymap.Action, msg tea.KeyMsg) bool {
	action, ok := m.action(ctx, msg)
	return ok && action == want
}

// cap is the key an action answers to, rendered for a footer: "" when the
// operator has unbound it, which a legend must leave out rather than print
// as an empty cap.
func (m *Model) cap(ctx keymap.Context, action keymap.Action) string {
	return keymap.Display(m.sideKey(ctx, action, m.km().Key(ctx, action)))
}

// hintKey is the key a hint names as the way to an action: the key it is on
// now, or for a list action the operator left unbound, the quick actions key
// and the action's name, which is how that action is still reached. "" when
// the keyboard cannot reach it at all.
func (m *Model) hintKey(ctx keymap.Context, action keymap.Action) string {
	if key := m.cap(ctx, action); key != "" {
		return key
	}
	if ctx != keymap.ContextList || quickActionsHidden[action] {
		return ""
	}
	palette := m.cap(keymap.ContextList, keymap.QuickActions)
	if palette == "" {
		return ""
	}
	return palette + " " + strings.ReplaceAll(string(action), "_", " ")
}

// keyHint is keyCap over hintKey: a badge or an empty state naming its way
// out. With no key to name, the label stands alone rather than beside a gap.
func (m *Model) keyHint(ctx keymap.Context, action keymap.Action, label string) string {
	if key := m.hintKey(ctx, action); key != "" {
		return keyCap(key, label)
	}
	return legendLabelStyle.Render(label)
}

// keyOr is the key an action is on, for a sentence that names it, or the
// words for the action when nothing is bound: "t finds it" must not read as
// " finds it" once the operator moves the archived view off t.
func (m *Model) keyOr(ctx keymap.Context, action keymap.Action, words string) string {
	if key := m.cap(ctx, action); key != "" {
		return key
	}
	return words
}

// pressTo is "press <key> to <what>" for a notice, or the bare instruction
// when the keyboard cannot reach the action: a sentence with an empty key in
// the middle of it reads as a typo.
func (m *Model) pressTo(ctx keymap.Context, action keymap.Action, what string) string {
	if key := m.hintKey(ctx, action); key != "" {
		return "press " + key + " to " + what
	}
	return what
}

// keysCap is every key an action answers to, joined: "y/↵" for a dialog that
// takes either. A legend for a screen where both spellings are natural names
// both, so a rebind that drops one drops it from the hint too.
func (m *Model) keysCap(ctx keymap.Context, action keymap.Action) string {
	var parts []string
	seen := map[string]bool{}
	for _, key := range m.km().Keys(ctx, action) {
		shown := keymap.Display(key)
		if seen[shown] || isShiftSpelling(key) {
			continue
		}
		seen[shown] = true
		parts = append(parts, shown)
	}
	return strings.Join(parts, "/")
}

// isShiftSpelling is the second spelling of a shifted letter: "shift+a"
// beside "A". A hint that printed both would read as two keys.
func isShiftSpelling(key string) bool {
	tail, ok := strings.CutPrefix(key, "shift+")
	return ok && len(tail) == 1
}

// pairCap renders two actions that share a modifier as one chord: "alt+↑↓"
// rather than "alt+↑/alt+↓". Keys that do not share one are joined plainly.
func (m *Model) pairCap(ctx keymap.Context, a, b keymap.Action) string {
	first, second := m.cap(ctx, a), m.cap(ctx, b)
	if first == "" || second == "" {
		return first + second
	}
	i, j := strings.LastIndex(first, "+"), strings.LastIndex(second, "+")
	if i > 0 && i == j && first[:i] == second[:j] {
		return first + second[j+1:]
	}
	return first + "/" + second
}

// rebind moves an action onto keys and writes the file. The map is replaced
// only when the rebind was accepted, so a refused one leaves the board on
// the keys it was already answering.
func (m *Model) rebind(ctx keymap.Context, action keymap.Action, keys []string) []string {
	next, problems := m.km().Rebind(ctx, action, keys)
	var refusals []string
	for _, problem := range problems {
		if problem.Context == ctx && problem.Action == action {
			refusals = append(refusals, problem.Error())
		}
	}
	if len(refusals) > 0 {
		return refusals
	}
	m.keys = next
	return m.saveKeys()
}

// resetBinding puts one action back on its default keys.
func (m *Model) resetBinding(ctx keymap.Context, action keymap.Action) []string {
	next, problems := m.km().Reset(ctx, action)
	m.keys = next
	notes := m.saveKeys()
	for _, problem := range problems {
		notes = append(notes, problem.Error())
	}
	return notes
}

// saveKeys writes the current map's overrides to the operator's file.
func (m *Model) saveKeys() []string {
	dir := m.configDir()
	if dir == "" {
		return []string{"no config directory: the rebind is live but not saved"}
	}
	if err := keymap.Save(dir, m.km().Overrides()); err != nil {
		return []string{"could not write " + keymap.Path(dir) + ": " + err.Error()}
	}
	return nil
}

// fullCap is the key spelled out, which is what the focused footer wants:
// its whole tier is chords, and "^q / ^\" is a row of punctuation where
// "ctrl+q / ctrl+\" is a sentence. The list footer compacts instead,
// because there it is one key among twenty letters.
func (m *Model) fullCap(ctx keymap.Context, action keymap.Action) string {
	return m.cap(ctx, action)
}

// capJoinFull is capJoin in the spelled-out spelling.
func (m *Model) capJoinFull(ctx keymap.Context, sep string, actions ...keymap.Action) string {
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		if key := m.fullCap(ctx, action); key != "" {
			parts = append(parts, key)
		}
	}
	return strings.Join(parts, sep)
}

// The footer's own spelling of a key. It is tighter than the key map's --
// "^n" rather than "ctrl+n" -- because the legend is one row of a frame the
// preview box sits under, and a second row there resizes every session's
// pane. The key map has room to spell chords out and does.
func (m *Model) tightCap(ctx keymap.Context, action keymap.Action) string {
	return keymap.Compact(m.sideKey(ctx, action, m.km().Key(ctx, action)))
}

// capJoin renders several actions as one legend cap: "x/X" for end and end
// all. An unbound action drops out, so a footer built this way never prints
// a separator with nothing on one side of it.
func (m *Model) capJoin(ctx keymap.Context, sep string, actions ...keymap.Action) string {
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		if key := m.tightCap(ctx, action); key != "" {
			parts = append(parts, key)
		}
	}
	return strings.Join(parts, sep)
}

// navCap is the pair of directions as the footer prints them: the arrows
// first and the letters after, "↑↓/kj", or just the arrows when the letters
// have been rebound away. Both actions' spellings are read in the same
// order, so up's alternative sits over down's rather than under it.
func (m *Model) navCap(ctx keymap.Context) string {
	up, down := m.km().Keys(ctx, keymap.CursorUp), m.km().Keys(ctx, keymap.CursorDown)
	var runs []string
	for i := 0; i < len(up) || i < len(down); i++ {
		run := ""
		if i < len(up) {
			run += keymap.Compact(up[i])
		}
		if i < len(down) {
			run += keymap.Compact(down[i])
		}
		if run != "" {
			runs = append(runs, run)
		}
	}
	return strings.Join(runs, "/")
}
