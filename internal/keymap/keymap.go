// Package keymap is every key the manager claims, written down once.
//
// Before this package a binding existed only as a string literal in the
// switch that answered it and a second literal in the key map that described
// it, so a key could be moved only by editing Go, and the two literals could
// disagree about what was bound. Here an action is named, carries its own
// default keys and its own label, and the switches ask which action a press
// stands for rather than comparing it to a letter. That is what makes a
// binding remappable: the operator's file replaces the keys of an action,
// and the handler, the footer and the key map all follow it because none of
// them held a literal in the first place.
//
// Contexts, not one flat map. The same letter means different things
// depending on what is on screen -- a switches the account on the list and
// ticks every row in the restore offer -- so a binding belongs to the screen
// that answers it, and a rebind in one leaves the others alone.
//
// What is deliberately NOT here: the keys that make typing work. Enter, esc,
// backspace, tab and the arrows inside a text field are the shape of a form
// rather than shortcuts over it, and an override that took esc away from a
// dialog would leave it unanswerable. Same for the digits that name a group
// and the ctrl+alt chord the snippets file owns, along with the one snippet key
// that sits outside it: those namespaces are reserved, and an override that
// reaches into them is refused.
package keymap

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Context is the screen a binding belongs to. A press is resolved against
// exactly one.
type Context string

const (
	ContextList      Context = "list"
	ContextFocus     Context = "focus"
	ContextNameSweep Context = "name_sweep"
	ContextRestore   Context = "restore"
	ContextWelcome   Context = "welcome"
	ContextConfirm   Context = "confirm"
)

// Contexts is every context in the order the key map lists them.
var Contexts = []Context{
	ContextList, ContextFocus,
	ContextNameSweep, ContextRestore, ContextWelcome, ContextConfirm,
}

// Action names what a key does. It is the identifier the operator's file
// writes and the switches compare against, so it is stable in a way a key
// never is.
type Action string

// Binding is one action: where it lives, what it is called, and the keys it
// answers to unless the operator says otherwise.
type Binding struct {
	Context Context
	Action  Action
	// Keys are the defaults. More than one is normal rather than
	// exceptional: a shifted letter arrives as "W" from one terminal and
	// "shift+w" from another, and both spellings have to reach the same
	// action. The first is the one every label prints, so it is the
	// spelling a reader should see -- "K", not "shift+up".
	Keys []string
	// Label is what the key file calls this action, written beside it in the
	// commented reference a first run leaves behind.
	Label string
	// Required marks an action a screen cannot be worked without: its way
	// out, its cursor, the answer to its question. It can be moved onto
	// other keys, but it cannot be left on none -- an unbound Close is a
	// screen with no way off it.
	Required bool
}

// Alias is a name an action was written under before: one a key file may
// still carry from before an extension took the action over or renamed it.
// An override under From applies to To, and is reported as renamed rather
// than unknown; the next save writes it under To's own name.
type Alias struct {
	Context Context
	From    Action
	To      Action
}

// Map is the resolved key map: defaults with the operator's overrides
// applied, indexed both ways.
type Map struct {
	byKey    map[Context]map[string]Action
	byAction map[Context]map[Action][]string
	// order preserves the catalog's order per context, which is the order
	// the key map screen reads in.
	order map[Context][]Action
	// catalog is Catalog plus the extra bindings this map accepted, and
	// contexts is Contexts plus the screens those added. extra is kept as
	// given, so a rebind resolves against the same bindings again.
	catalog  []Binding
	contexts []Context
	extra    []Binding
	aliasSet []Alias
	// aliases resolves an old action name, per screen, to the action that
	// took it over.
	aliases map[Context]map[Action]Action
	// foreign is every override naming a screen or an action this build
	// does not have. It is written back unchanged on save, so a rebind made
	// while an extension is switched off does not cost that extension the
	// keys the operator gave it.
	foreign Overrides
}

// Overrides is the operator's file, as contexts of actions to keys. An empty
// key list unbinds the action; an absent action keeps its default.
type Overrides map[Context]map[Action][]string

// Problem is one thing wrong with an override. A problem never stops the
// board from starting: the binding it names falls back to its default and
// the operator is told, because a typo in a key file must not cost anybody
// their session list.
type Problem struct {
	Context Context
	Action  Action
	Key     string
	Reason  string
}

func (p Problem) Error() string {
	where := string(p.Context)
	if p.Action != "" {
		where += "." + string(p.Action)
	}
	if p.Key != "" {
		return fmt.Sprintf("%s: %q %s", where, p.Key, p.Reason)
	}
	return where + ": " + p.Reason
}

// New resolves the catalog against overrides. It always returns a usable
// map; every override it could not honour comes back as a problem and leaves
// that action on its default.
func New(overrides Overrides) (*Map, []Problem) {
	return NewWith(overrides, nil)
}

// NewWith is New over the catalog plus extra bindings: the keys a build's
// extensions add. An extra binding may join a screen the catalog has or name
// a screen of its own. It comes after every catalog binding, so where its
// default key is one a catalog action already holds, the catalog keeps it and
// the collision is reported. A binding that would redefine an action already
// on its screen is refused as a whole.
func NewWith(overrides Overrides, extra []Binding) (*Map, []Problem) {
	return NewWithAliases(overrides, extra, nil)
}

// NewWithAliases is NewWith where some actions also answer to old names in
// the operator's file.
func NewWithAliases(overrides Overrides, extra []Binding, aliases []Alias) (*Map, []Problem) {
	m := &Map{
		aliasSet: append([]Alias(nil), aliases...),
		byKey:    map[Context]map[string]Action{},
		byAction: map[Context]map[Action][]string{},
		order:    map[Context][]Action{},
		contexts: append([]Context(nil), Contexts...),
		extra:    append([]Binding(nil), extra...),
	}
	for _, binding := range Catalog {
		m.add(binding)
	}
	problems := m.addExtra(extra)
	problems = append(problems, m.addAliases(aliases)...)
	problems = append(problems, m.apply(overrides)...)
	m.reindex()
	return m, problems
}

func (m *Map) add(binding Binding) {
	if m.byAction[binding.Context] == nil {
		m.byAction[binding.Context] = map[Action][]string{}
		m.byKey[binding.Context] = map[string]Action{}
	}
	m.byAction[binding.Context][binding.Action] = append([]string(nil), binding.Keys...)
	m.order[binding.Context] = append(m.order[binding.Context], binding.Action)
	m.catalog = append(m.catalog, binding)
}

// addExtra checks the extra bindings and adds the ones it can. A binding on a
// catalog screen may not be required: that screen is already workable, and a
// required extra would be a key no rebind could take away from it.
func (m *Map) addExtra(extra []Binding) []Problem {
	var problems []Problem
	for _, binding := range extra {
		refuse := func(reason string) {
			problems = append(problems, Problem{Context: binding.Context, Action: binding.Action, Reason: reason})
		}
		switch {
		case !namePattern.MatchString(string(binding.Context)):
			refuse("is not a screen name: lower case letters, digits and '_'")
			continue
		case !namePattern.MatchString(string(binding.Action)):
			refuse("is not an action name: lower case letters, digits and '_'")
			continue
		case binding.Required && slices.Contains(Contexts, binding.Context):
			refuse("cannot be required on a screen the board already has")
			continue
		}
		if _, taken := m.byAction[binding.Context][binding.Action]; taken {
			refuse("is already an action on this screen")
			continue
		}
		keys, bad := validateKeys(binding.Context, binding.Action, binding.Keys)
		problems = append(problems, bad...)
		binding.Keys = keys
		if !slices.Contains(m.contexts, binding.Context) {
			m.contexts = append(m.contexts, binding.Context)
		}
		m.add(binding)
	}
	return problems
}

// addAliases indexes the aliases. One whose target is not an action on its
// screen, whose old name is itself an action there, or whose old name
// another action already claims, is refused: an override under it could
// only mean one of them.
func (m *Map) addAliases(aliases []Alias) []Problem {
	var problems []Problem
	m.aliases = map[Context]map[Action]Action{}
	for _, alias := range aliases {
		refuse := func(reason string) {
			problems = append(problems, Problem{Context: alias.Context, Action: alias.From, Reason: reason})
		}
		if !namePattern.MatchString(string(alias.From)) {
			refuse("is not an action name: lower case letters, digits and '_'")
			continue
		}
		if _, known := m.byAction[alias.Context][alias.To]; !known {
			refuse("cannot be an alias of " + string(alias.To) + ": it is not an action on this screen")
			continue
		}
		if _, taken := m.byAction[alias.Context][alias.From]; taken {
			refuse("cannot be an alias of " + string(alias.To) + ": it is an action on this screen")
			continue
		}
		if owner, taken := m.aliases[alias.Context][alias.From]; taken {
			if owner != alias.To {
				refuse("cannot be an alias of " + string(alias.To) + ": it is already one of " + string(owner))
			}
			continue
		}
		if m.aliases[alias.Context] == nil {
			m.aliases[alias.Context] = map[Action]Action{}
		}
		m.aliases[alias.Context][alias.From] = alias.To
	}
	return problems
}

// namePattern is what a screen or an action added from outside the catalog
// may be called: the shape every catalog name already has, which keeps it a
// bare key in the key file.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// apply writes the overrides it accepts over the defaults. Contexts are
// walked in catalog order and actions in theirs, so two runs over the same
// file report the same problems in the same order.
func (m *Map) apply(overrides Overrides) []Problem {
	var problems []Problem
	m.foreign = Overrides{}
	for _, ctx := range sortedContexts(overrides) {
		if !slices.Contains(m.contexts, ctx) {
			problems = append(problems, Problem{Context: ctx,
				Reason: "is not a screen this build has; its bindings are ignored"})
			m.keepForeign(ctx, overrides[ctx])
		}
	}
	for _, ctx := range m.contexts {
		wanted, ok := overrides[ctx]
		if !ok {
			continue
		}
		for _, action := range sortedActions(wanted) {
			keys := wanted[action]
			if renamed, ok := m.aliases[ctx][action]; ok {
				if _, own := wanted[renamed]; own {
					problems = append(problems, Problem{Context: ctx, Action: action,
						Reason: "was renamed to " + string(renamed) + ", whose own binding is used"})
					continue
				}
				problems = append(problems, Problem{Context: ctx, Action: action,
					Reason: "was renamed to " + string(renamed) + "; its keys apply there"})
				action = renamed
			} else if reason, gone := retired[action]; gone {
				problems = append(problems, Problem{Context: ctx, Action: action, Reason: reason})
				continue
			}
			if _, known := m.byAction[ctx][action]; !known {
				problems = append(problems, Problem{Context: ctx, Action: action,
					Reason: "is not an action on this screen"})
				m.keepForeign(ctx, map[Action][]string{action: keys})
				continue
			}
			cleaned, bad := validateKeys(ctx, action, keys)
			if len(bad) > 0 {
				problems = append(problems, bad...)
				continue
			}
			if len(cleaned) == 0 && m.required(ctx, action) {
				problems = append(problems, Problem{Context: ctx, Action: action,
					Reason: "cannot be left unbound: it is how this screen is worked"})
				continue
			}
			m.byAction[ctx][action] = cleaned
		}
	}
	problems = append(problems, m.collisions()...)
	return problems
}

// collisions finds two actions on one screen left holding the same key, and
// gives the key to whichever the catalog lists first -- an override that
// wants a key another action holds has to move that one too, and until it
// does the screen keeps working rather than losing a binding silently.
func (m *Map) collisions() []Problem {
	var problems []Problem
	for _, ctx := range m.contexts {
		held := map[string]Action{}
		for _, action := range m.order[ctx] {
			kept := m.byAction[ctx][action][:0]
			for _, key := range m.byAction[ctx][action] {
				if owner, taken := held[key]; taken {
					problems = append(problems, Problem{Context: ctx, Action: action, Key: key,
						Reason: "is already bound to " + string(owner)})
					continue
				}
				held[key] = action
				kept = append(kept, key)
			}
			m.byAction[ctx][action] = kept
		}
	}
	return problems
}

func (m *Map) reindex() {
	for ctx, actions := range m.byAction {
		index := make(map[string]Action, len(actions))
		for _, action := range m.order[ctx] {
			for _, key := range actions[action] {
				if _, taken := index[key]; !taken {
					index[key] = action
				}
			}
		}
		m.byKey[ctx] = index
	}
}

// Action answers which action a press stands for on this screen.
func (m *Map) Action(ctx Context, key string) (Action, bool) {
	action, ok := m.byKey[ctx][key]
	return action, ok
}

// Keys are every key bound to an action, in the order they were written.
func (m *Map) Keys(ctx Context, action Action) []string {
	return append([]string(nil), m.byAction[ctx][action]...)
}

// Key is the one key a label shows for an action: the first bound, or ""
// when the operator has unbound it. A surface that renders a "" key must
// leave the row out rather than print an empty cap.
func (m *Map) Key(ctx Context, action Action) string {
	keys := m.byAction[ctx][action]
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// Cap is Key rendered the way a footer prints it: ↵ rather than "enter".
func (m *Map) Cap(ctx Context, action Action) string { return Display(m.Key(ctx, action)) }

// Bound reports whether an action has any key at all.
func (m *Map) Bound(ctx Context, action Action) bool { return len(m.byAction[ctx][action]) > 0 }

// Overrides is the map read back as a file: only what differs from the
// defaults, which keeps a rebind of one key from freezing every other
// binding at today's default.
func (m *Map) Overrides() Overrides {
	out := Overrides{}
	for ctx, set := range m.foreign {
		out[ctx] = map[Action][]string{}
		for action, keys := range set {
			out[ctx][action] = append([]string(nil), keys...)
		}
	}
	for _, binding := range m.catalog {
		current := m.byAction[binding.Context][binding.Action]
		if sameKeys(current, binding.Keys) {
			continue
		}
		if out[binding.Context] == nil {
			out[binding.Context] = map[Action][]string{}
		}
		out[binding.Context][binding.Action] = append([]string(nil), current...)
	}
	return out
}

// Rebind returns a copy of the map with one action moved onto keys. It is
// the in-app editor's whole write path: the copy is validated before it is
// installed, so a rebind that would collide is reported without the board
// ever holding a broken map.
func (m *Map) Rebind(ctx Context, action Action, keys []string) (*Map, []Problem) {
	next := m.Overrides()
	if next[ctx] == nil {
		next[ctx] = map[Action][]string{}
	}
	next[ctx][action] = keys
	// An action rebound onto a key another one holds takes it: the operator
	// pressed it deliberately, and refusing would mean asking them to unbind
	// the other one first. The action that loses it is reported, so it never
	// happens silently.
	var displaced []Problem
	for _, key := range keys {
		owner, taken := m.Action(ctx, key)
		if !taken || owner == action {
			continue
		}
		remaining := without(m.Keys(ctx, owner), key)
		// Taking the last key off an action the screen cannot be worked
		// without is refused here rather than downstream: the resolve would
		// restore that action's defaults, both would claim the key, and the
		// rebind the operator just asked for would quietly not happen.
		if len(remaining) == 0 && m.required(ctx, owner) {
			return m, []Problem{{Context: ctx, Action: action, Key: key,
				Reason: "is the last key on " + string(owner) + ", which this screen needs"}}
		}
		next[ctx][owner] = remaining
		displaced = append(displaced, Problem{Context: ctx, Action: owner, Key: key,
			Reason: "moved to " + string(action)})
	}
	rebuilt, problems := NewWithAliases(next, m.extra, m.aliasSet)
	for _, problem := range problems {
		if problem.Context == ctx && problem.Action == action {
			return m, problems
		}
	}
	return rebuilt, append(problems, displaced...)
}

// Reset puts one action back on its defaults.
func (m *Map) Reset(ctx Context, action Action) (*Map, []Problem) {
	next := m.Overrides()
	delete(next[ctx], action)
	return NewWithAliases(next, m.extra, m.aliasSet)
}

// Contexts is every screen this map resolves keys for: the catalog's, then
// the ones its extra bindings added, in the order they were added.
func (m *Map) Contexts() []Context { return append([]Context(nil), m.contexts...) }

// Bindings is every binding on one screen, in catalog order, each carrying
// the keys it answers to now rather than its defaults.
func (m *Map) Bindings(ctx Context) []Binding {
	var out []Binding
	for _, binding := range m.catalog {
		if binding.Context != ctx {
			continue
		}
		binding.Keys = m.Keys(ctx, binding.Action)
		out = append(out, binding)
	}
	return out
}

func (m *Map) keepForeign(ctx Context, set map[Action][]string) {
	if m.foreign[ctx] == nil {
		m.foreign[ctx] = map[Action][]string{}
	}
	for action, keys := range set {
		m.foreign[ctx][action] = append([]string(nil), keys...)
	}
}

func without(keys []string, drop string) []string {
	out := keys[:0]
	for _, key := range keys {
		if key != drop {
			out = append(out, key)
		}
	}
	return out
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedContexts(overrides Overrides) []Context {
	out := make([]Context, 0, len(overrides))
	for ctx := range overrides {
		out = append(out, ctx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedActions(set map[Action][]string) []Action {
	out := make([]Action, 0, len(set))
	for action := range set {
		out = append(out, action)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// validateKeys checks one override's keys against what a terminal can
// deliver and what another mechanism already owns.
func validateKeys(ctx Context, action Action, keys []string) ([]string, []Problem) {
	var problems []Problem
	seen := map[string]bool{}
	cleaned := make([]string, 0, len(keys))
	for _, raw := range keys {
		key := canonicalKey(strings.TrimSpace(raw))
		if key == "" {
			problems = append(problems, Problem{Context: ctx, Action: action, Key: raw,
				Reason: "is not a key"})
			continue
		}
		if seen[key] {
			continue
		}
		if reason, bad := reserved(ctx, key); bad {
			problems = append(problems, Problem{Context: ctx, Action: action, Key: key, Reason: reason})
			continue
		}
		seen[key] = true
		cleaned = append(cleaned, key)
	}
	return cleaned, problems
}

// reserved names the keys no override may take, and says who holds them.
//
// The snippet keys are spelled out here rather than imported: this package is
// a leaf that the snippets file's format has no business reaching into, and an
// override landing on one of them would not fail loudly -- the handlers read
// snippets before their own bindings, so the rebound action would simply never
// fire.
func reserved(ctx Context, key string) (string, bool) {
	if strings.HasPrefix(key, "ctrl+alt+") {
		return "is in the chord the snippets file owns", true
	}
	if key == "alt+§" || key == "±" {
		return "is a snippet key outside that chord", true
	}
	if key == "ctrl+c" {
		return "quits from every screen and cannot be moved", true
	}
	if ctx == ContextList && len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		return "names a group on the list", true
	}
	return "", false
}

// retired are actions a key file may still name from an older release. Each
// is reported, with where its job went, and otherwise ignored like any other
// problem: the file still loads, and the next rebind saves it without them.
var retired = map[Action]string{
	"approve": "was removed; bind its sentence to ± as a snippet in snippets.json",
}

// required reports whether an action is one this screen cannot be worked
// without, which is what forbids unbinding it.
func (m *Map) required(ctx Context, action Action) bool {
	for _, binding := range m.catalog {
		if binding.Context == ctx && binding.Action == action {
			return binding.Required
		}
	}
	return false
}
