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
// Bindings live under one reserved chord, ctrl+alt. That is not decoration: in
// a focused session every key the manager does not claim is forwarded to the
// agent, so a snippet on a plain letter would eat that letter while you type.
// ctrl+alt is a chord no agent CLI binds, so claiming the whole namespace at
// once costs the pane nothing and leaves the operator the entire alphabet.
//
// Two keys sit outside that chord, both on the physical key left of 1, where
// the triage keys already are, so the sentences sent most often belong under
// that hand too. They are exceptions rather than a second namespace.
//
// § binds on alt alone. It is safe on the same terms the chord is -- no agent
// CLI binds alt+§ either -- and the alt is what keeps it distinct from the
// bare § that hands over. It cannot join the chord instead: ctrl+§ never
// reaches the manager through tmux, and where tmux makes it up itself it
// arrives as a bare §, the handover key. Why alt and not ctrl is measured, not
// chosen: see SectionKey.
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

// Chord prefixes every snippet key. See the package comment for why the
// namespace is reserved rather than picked per snippet.
const Chord = "ctrl+alt+"

// SectionKey is the one key that binds outside that chord, and SectionChord is
// what it binds on instead.
//
// alt rather than ctrl because ctrl+§ cannot be delivered at all, which is
// measured rather than reasoned about. There is no control byte for §, so a
// terminal can report ctrl+§ only as an extended key -- CSI 167;5u, or
// CSI 27;5;167~ under modifyOtherKeys -- and tmux 3.4 drops both, as it drops
// every CSI-u event above ASCII. The manager always runs inside a pane, so
// that is the whole path. Worse than silence: where tmux synthesises the key
// itself, ctrl+§ comes out as a bare §, so reaching for the snippet would hand
// the session over instead.
//
// alt survives on the one path a real keypress takes. alt+§ goes as ESC before
// the key's own bytes, ESC C2 A7, which tmux forwards untouched and the decoder
// reads back as alt+§, distinct from §. Its extended forms would not survive --
// tmux reduces alt+§ in CSI-u or modifyOtherKeys to a lone ESC -- but tmux
// never asks the outer terminal for either: it caps modifyOtherKeys at mode 1,
// which leaves alt on printable keys to the legacy prefix, and it never
// enables the kitty protocol. TestSectionKeyIsReachable pins the decoder's
// half of this. The same holds for ± and for any other non-ASCII key: ctrl
// cannot carry one, alt can.
const (
	SectionKey   = "§"
	SectionChord = "alt+"
)

// PlusMinusKey is the one snippet key with no modifier at all: shifted §, so
// the same physical key as the handover. It carries no chord because it needs
// none -- nothing behind the manager binds ±, and the manager binds nothing
// on it -- and because a bare key is what the hand already expects there.
//
// Being bare, it is also text, so the quick prompt bar leaves it to the input
// rather than reading it as the snippet: see Bare.
const PlusMinusKey = "±"

// Snippet is one key and what it sends.
type Snippet struct {
	// Key is the bare key, without the chord: "c" binds ctrl+alt+c.
	Key string `json:"key"`
	// Label is what the key map, the footer and the quick bar call it. Empty
	// falls back to the text itself, which is usually short enough to read.
	Label string `json:"label,omitempty"`
	// Text is what is typed into the session and submitted.
	Text string `json:"text"`
}

// Binding is the key as the TUI reports it, ready to compare against a
// keypress.
func (s Snippet) Binding() string {
	switch s.Key {
	case SectionKey:
		return SectionChord + SectionKey
	case PlusMinusKey:
		return PlusMinusKey
	}
	return Chord + s.Key
}

// Bare reports whether the binding is a plain character, which a text input
// has to keep as the character it types.
func (s Snippet) Bare() bool { return s.Key == PlusMinusKey }

// IsBinding reports whether a keypress could name a snippet at all. The
// manager asks this of every key it sees, so it settles the overwhelmingly
// common answer -- an ordinary key, which is not ours -- without walking the
// set.
func IsBinding(binding string) bool {
	return strings.HasPrefix(binding, Chord) || binding == SectionChord+SectionKey ||
		binding == PlusMinusKey
}

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
// Problems travel with the set rather than replacing it, because these are
// bindings and a rejected one is otherwise a key that silently does nothing.
// One typo must not cost the operator the other six snippets, and it must not
// be invisible either: the viewer prints these, so "why did ctrl+alt+1 stop
// working" is answered on the screen that lists the snippets.
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

// Defaults are written on first run and are then the user's to edit. The first
// three are generic asks rather than a guess at anyone's workflow: the file
// exists to be rewritten, and an empty one would not show what an entry looks
// like. § is the exception and the reason the alt binding exists at all.
//
// Nothing rewrites a file that already exists, so an operator who has one adds
// the § entry by hand. That is the same deliberate rule Load follows, and it
// costs a default reaching them late rather than their own edits being lost.
func Defaults() []Snippet {
	return []Snippet{
		{Key: "c", Label: "continue", Text: "continue"},
		{Key: "t", Label: "run tests", Text: "run the tests and report what failed"},
		{Key: "p", Label: "open a PR", Text: "open a pull request for this work"},
		{Key: SectionKey, Label: "progress", Text: progressText},
	}
}

// Load reads the snippets file, writing the defaults first when it does not
// exist yet.
//
// A file that exists but will not parse returns an error and NO snippets rather
// than silently substituting the defaults: the user edited that file on
// purpose, and binding keys to a version of it they deleted would be worse than
// making them fix a comma. An entry that parses but cannot bind is a Problem —
// see Set.
func Load(dir string) (Set, error) {
	path := Path(dir)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		defaults := Defaults()
		if encoded, err := json.MarshalIndent(defaults, "", "  "); err == nil {
			_ = os.WriteFile(path, append(encoded, '\n'), 0o644)
		}
		return Set{Snippets: defaults}, nil
	}
	if err != nil {
		return Set{}, err
	}
	var parsed []Snippet
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Set{}, fmt.Errorf("%s: %w", path, err)
	}
	return validate(parsed), nil
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
			// Letters, § and ±, and this is a terminal limit rather than a taste.
			// A terminal has no distinct byte for ctrl+alt+1 or ctrl+alt+, and
			// sends the digit or the comma unchanged, so such a binding would
			// never fire and would take a plain key away from the agent if it
			// did. Letters are delivered as an ESC-prefixed control byte; §
			// and ± earn their places off the chord, and widen nothing else.
			set.Problems = append(set.Problems,
				entry(i, snip)+"key "+quote(snip.Key)+" must be a single letter a-z, "+SectionKey+" or "+PlusMinusKey+": "+
					"the terminal sends no distinct code for "+Chord+snip.Key)
		case unreachable[snip.Key] != "":
			// Two letters are not reachable through this chord and never will
			// be. ctrl+i and ctrl+m ARE the Tab and Enter bytes -- the same
			// equivalence focusNamedKeys relies on to send them -- so the
			// terminal writes ESC 0x09 and ESC 0x0d, and the decoder reads
			// back alt+tab and alt+enter. There is no key press that produces
			// ctrl+alt+i, so a binding on it would look correct in the file
			// and never fire once.
			set.Problems = append(set.Problems,
				entry(i, snip)+Chord+snip.Key+" cannot be typed: the terminal sends it as "+
					unreachable[snip.Key]+". Pick another letter.")
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

// unreachable are the letters this chord cannot carry, and what the terminal
// sends instead. Verified against the decoder rather than reasoned about:
// TestChordIsReachable walks all twenty-six.
//
// ctrl+§ belongs in the same list and is not in it, because the key rule never
// lets it get this far. It fails for another reason: i and m arrive as the
// wrong key, § does not arrive at all. There is no control byte for a non-ASCII
// key, so ctrl+§ exists only as an extended key, and tmux 3.4 drops every
// extended key above ASCII before the decoder sees it. That is why § binds on
// alt, and SectionKey has the rest.
var unreachable = map[string]string{
	"i": "alt+tab",
	"m": "alt+enter",
}

// legalKey is what may bind: the chord's alphabet, plus the two keys that bind
// outside it.
func legalKey(key string) bool {
	return singleLetter(key) || key == SectionKey || key == PlusMinusKey
}

func singleLetter(key string) bool {
	runes := []rune(key)
	return len(runes) == 1 && runes[0] >= 'a' && runes[0] <= 'z'
}

func quote(s string) string { return "“" + s + "”" }
