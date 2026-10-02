// Package snippets is the operator's own one-key answers: a user-editable file
// of key-and-text pairs, each of which types its text into the session in front
// of you and submits it.
//
// A triage pass types the same few sentences into session after session, and
// typing them by hand was the slowest part of the pass. Every operator has a
// handful of their own, and a fleet answered mostly in stock phrases wants
// them all on a key.
//
// The file lives in the config directory beside config.toml, is written once
// with a starting set, and is then the user's to edit. Nothing rewrites it.
//
// Snippets fire from one menu, the hotkey menu, which a leader key opens from
// the list and from inside a focused session. The menu is the whole namespace:
// a bare key there names its snippet, so no modifier chord is reserved
// anywhere and nothing the operator presses while typing can shadow -- or be
// shadowed by -- a snippet binding. That is the entire reason there is no
// chord anymore: the old ctrl+alt namespace collided with operator tooling,
// and every chord off it that a terminal can actually deliver through tmux
// collides worse. ctrl+super never reaches a terminal TUI at all (macOS keeps
// Cmd for its own shortcuts; the only encoding is a kitty-protocol CSI-u tmux
// never forwards), and ctrl+shift arrives with the shift stripped, as the
// plain ctrl key. A leader plus a visible menu is what survives the wire.
//
// One key sits outside the menu, on the physical key left of 1 where the
// triage keys already are, so the sentence sent most often belongs under that
// hand too. It is an exception rather than a second namespace.
//
// ± binds bare. The manager claims no ± of its own, and it is a character no
// agent CLI wants, so taking it costs the pane a key it was never sent. See
// PlusMinusKey.
package snippets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// SectionKey is the menu key for the progress summary, on the physical key
// left of 1 where the triage keys already are, so the sentence sent most
// often belongs under that hand too. It reads as a bare key in the menu like
// every other snippet; a bare § outside the menu still hands over, which is
// why the menu is the only place it answers.
const SectionKey = "§"

// PlusMinusKey is the one snippet key with no modifier at all: shifted §, so
// the same physical key as the handover. It carries no chord because it needs
// none -- nothing behind the manager binds ±, and the manager binds nothing
// on it -- and because a bare key is what the hand already expects there.
//
// Being bare, it is also text, so a text input keeps it as the character it
// types rather than reading it as the snippet: see Bare.
const PlusMinusKey = "±"

// Snippet is one key and what it sends.
type Snippet struct {
	// Key is the menu key: a single letter a-z, or one of the two keys on
	// the physical key left of 1, § and ±.
	Key string `json:"key"`
	// Label is what the key map, the footer and the quick bar call it. Empty
	// falls back to the text itself, which is usually short enough to read.
	Label string `json:"label,omitempty"`
	// Text is what is typed into the session.
	Text string `json:"text"`
	// AutoSubmit says whether the key presses Enter after typing Text. Every
	// entry the board writes spells it out, so the file shows the switch
	// exists; an entry that leaves it out submits, as every snippet did
	// before it was a choice. false types the text and leaves it in the
	// prompt for the operator to finish.
	AutoSubmit *bool `json:"autoSubmit"`
}

// Submits reports whether the snippet presses Enter after its text.
func (s Snippet) Submits() bool { return s.AutoSubmit == nil || *s.AutoSubmit }

// Binding is the key as the TUI reports it, ready to compare against a
// keypress. Every snippet binds its bare key: the menu reads bare keys, and
// ± binds bare everywhere.
func (s Snippet) Binding() string { return s.Key }

// Bare reports whether the binding is a plain character, which a text input
// has to keep as the character it types.
func (s Snippet) Bare() bool { return s.Key == PlusMinusKey }

// IsBinding reports whether a keypress could name a snippet outside the menu.
// Only the bare ± key binds there; every other snippet answers to its bare
// key in the menu alone, so an ordinary key is never a snippet anywhere the
// operator is typing. The manager asks this of every key it sees, so it
// settles that overwhelmingly common answer without walking the set.
func IsBinding(binding string) bool { return binding == PlusMinusKey }

// Title is what a surface should call this snippet.
func (s Snippet) Title() string {
	if s.Label != "" {
		return s.Label
	}
	return s.Text
}

// Quoted is the snippet's text as commentary names it.
func (s Snippet) Quoted() string { return "“" + s.Text + "”" }

// Set is a loaded file: the snippets that bound, and the entries that did not.
//
// Problems travel with the set rather than replacing it, because a rejected
// key is otherwise one that silently does nothing. One typo must not cost the
// operator the other six snippets, and it must not be invisible either: the
// viewer prints these, so "why did 1 stop working" is answered on the screen
// that lists the snippets.
type Set struct {
	Snippets []Snippet
	Problems []string
}

// Get returns the snippet a keypress names.
func (s Set) Get(binding string) (Snippet, bool) {
	for _, snip := range s.Snippets {
		if snip.Binding() == binding {
			return snip, true
		}
	}
	return Snippet{}, false
}

// Path is the snippets file inside the config directory.
func Path(dir string) string { return filepath.Join(dir, "snippets.json") }

// progressText is the sentence on §, and it is deliberately the operator's own
// words rather than a synthesised instruction.
//
// It is pressed mostly on a session that is itself running a fan-out of
// children, so what it asks for is the whole workstream -- every pull request
// and where it stands -- rather than the file in front of that agent. It asks
// for an answer on the pane and nothing else: a key that could send something
// outward would be one the operator had to think before pressing.
const progressText = "summarise all current progress in bullet points, " +
	"including every PR link and its status, and what the next steps are " +
	"— or, if all the work here is finished, say so"

// Defaults are written on first run and are then the user's to edit. They are
// the answers the v1 inbox offered as one-tap shortcuts, plus the handful an
// operator sends most -- yes, continue, explain again -- because
// the file exists to be rewritten, and an empty one would not show what an
// entry looks like.
//
// Nothing rewrites a file that already exists, so an operator who has one adds
// a new default by hand. That is the same deliberate rule Load follows, and it
// costs a default reaching them late rather than their own edits being lost.
func Defaults() []Snippet {
	submit := func(key, label, text string) Snippet {
		yes := true
		return Snippet{Key: key, Label: label, Text: text, AutoSubmit: &yes}
	}
	return []Snippet{
		submit("y", "yes", "yes"),
		submit("c", "continue", "continue"),
		submit("e", "explain like I'm 5", "I'm confused, explain like I'm 5"),
		submit("w", "wake up", "Auto wake-up: the previous turn died on a transient API error. "+
			"Continue where you left off."),
		submit(SectionKey, "progress", progressText),
	}
}

// Load reads the snippets file, writing the defaults first when it does not
// exist yet, and merges the distribution's entries under it: see
// UseDistribution.
//
// A file that exists but will not parse returns an error and NO snippets rather
// than silently substituting the defaults: the user edited that file on
// purpose, and binding keys to a version of it they deleted would be worse than
// making them fix a comma. An entry that parses but cannot bind is a Problem —
// see Set.
func Load(dir string) (Set, error) {
	path := Path(dir)
	supplied := currentDistribution()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		defaults := firstRun(supplied)
		if encoded, err := json.MarshalIndent(defaults, "", "  "); err == nil {
			_ = os.WriteFile(path, append(encoded, '\n'), 0o644)
		}
		if len(supplied) == 0 {
			return Set{Snippets: defaults}, nil
		}
		return validate(underlay(defaults, supplied)), nil
	}
	if err != nil {
		return Set{}, err
	}
	var parsed []Snippet
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Set{}, fmt.Errorf("%s: %w", path, err)
	}
	return validate(underlay(parsed, supplied)), nil
}

// validate keeps the entries that can bind and says why the rest cannot.
func validate(parsed []Snippet) Set {
	var set Set
	taken := map[string]int{}
	for i, snip := range parsed {
		snip.Key = strings.ToLower(strings.TrimSpace(snip.Key))
		snip.Label = strings.TrimSpace(snip.Label)
		// Only the text's edges: a snippet is a message, and an operator who
		// wrote one across several lines meant those lines.
		snip.Text = strings.TrimSpace(snip.Text)
		switch {
		case snip.Key == "":
			set.Problems = append(set.Problems, entry(i, snip)+"has no key")
		case !legalKey(snip.Key):
			// Letters, § and ±. The menu reads bare keys, so every letter
			// binds -- including i and m, which no chord could carry -- and
			// § and ± earn their places on the physical key left of 1.
			set.Problems = append(set.Problems,
				entry(i, snip)+"key "+quote(snip.Key)+" must be a single letter a-z, "+SectionKey+" or "+PlusMinusKey)
		case snip.Text == "":
			set.Problems = append(set.Problems, entry(i, snip)+"sends nothing")
		default:
			if first, dup := taken[snip.Key]; dup {
				set.Problems = append(set.Problems,
					entry(i, snip)+"repeats "+snip.Binding()+", already bound by entry "+
						strconv.Itoa(first+1))
				continue
			}
			taken[snip.Key] = i
			set.Snippets = append(set.Snippets, snip)
		}
	}
	// Ordered by key so every surface that lists them — the key map, the
	// footer, the quick bar — agrees, whatever order the file was written in.
	sort.Slice(set.Snippets, func(a, b int) bool {
		return set.Snippets[a].Key < set.Snippets[b].Key
	})
	return set
}

// entry names an offending line the way the file numbers it, so a problem
// points at something the operator can find by counting.
func entry(index int, snip Snippet) string {
	name := "entry " + strconv.Itoa(index+1)
	if snip.Label != "" {
		name += " (" + snip.Label + ")"
	}
	return name + ": "
}

// legalKey is what may bind in the menu: the alphabet, plus the two keys on
// the physical key left of 1.
func legalKey(key string) bool {
	return singleLetter(key) || key == SectionKey || key == PlusMinusKey
}

func singleLetter(key string) bool {
	runes := []rune(key)
	return len(runes) == 1 && runes[0] >= 'a' && runes[0] <= 'z'
}

func quote(s string) string { return "“" + s + "”" }
