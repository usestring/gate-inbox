// Package priority is how much the work in a session matters, on the one
// scale the whole manager speaks.
//
// The board already knows what every session is doing; status answers that.
// It had no answer for which of eight sessions waiting on the operator to
// answer first, and the mark that used to serve -- a boolean "this one
// before the others" -- could not tell the release from the customer's fix
// once more than one row carried it.
//
// Pure: no store, no clock, no terminal. Callers gather the evidence -- a
// goal's frontmatter, a session's declaration, a keypress -- and this
// decides what the tier is and where it sorts.
package priority

import (
	"fmt"
	"strings"
)

// Tier is the priority of the work, not of the session. Two panes on the
// same goal carry the same tier however differently they are behaving.
type Tier string

const (
	Urgent Tier = "urgent"
	High   Tier = "high"
	Medium Tier = "medium"
	Low    Tier = "low"
	Lower  Tier = "lower"
	Lowest Tier = "lowest"
	Unset  Tier = ""
)

// Order is highest-first: every tier the scale can hold, and the order a
// queue walks. A Scale picks the part of it the operator uses.
var Order = []Tier{Urgent, High, Medium, Low, Lower, Lowest}

// ranks are the sort keys, lower first. Unset is the neutral default.
var ranks = map[Tier]int{Urgent: 0, High: 1, Medium: 2, Unset: 3, Low: 4, Lower: 5, Lowest: 6}

// Level is the tier's signed distance from the default: +3 for urgent down
// to -3 for lowest, 0 for unset or an unknown value.
func (t Tier) Level() int {
	return ranks[Unset] - t.Rank()
}

// Deferred reports whether the tier sits below the default: the operator
// putting the work off on purpose.
func (t Tier) Deferred() bool { return t.Level() < 0 }

// MaxSteps is how many tiers the scale has on each side of the default.
const MaxSteps = 3

// Scale is how many raised and lowered tiers the operator works with. Few
// tiers are easier to keep straight than many, so the default is two above
// and one below; a scale never holds more than MaxSteps on a side.
type Scale struct {
	Raised  int
	Lowered int
}

// DefaultScale is two raised tiers (high, medium) and one lowered (low).
var DefaultScale = Scale{Raised: 2, Lowered: 1}

// Normalize keeps each side within 1..MaxSteps raised and 0..MaxSteps
// lowered. At least one raised tier stays, so the key always has something
// to set.
func (s Scale) Normalize() Scale {
	return Scale{Raised: min(max(s.Raised, 1), MaxSteps), Lowered: min(max(s.Lowered, 0), MaxSteps)}
}

// Tiers is the scale's cycle, highest first: the top Raised levels counted
// up from the default, then the first Lowered levels below it.
func (s Scale) Tiers() []Tier {
	s = s.Normalize()
	var tiers []Tier
	for _, tier := range Order {
		if level := tier.Level(); (level > 0 && level <= s.Raised) || (level < 0 && -level <= s.Lowered) {
			tiers = append(tiers, tier)
		}
	}
	return tiers
}

// Clamp maps a stored tier onto the scale. A tier past the scale's edge
// reads as the edge -- an urgent session under a two-tier scale is high --
// and a lowered tier on a scale with none reads as unset. Stored values are
// left alone, so widening the scale again restores them.
func (s Scale) Clamp(t Tier) Tier {
	s = s.Normalize()
	level := t.Level()
	switch {
	case level > s.Raised:
		level = s.Raised
	case level < -s.Lowered:
		level = -s.Lowered
	}
	return atLevel(level)
}

// Next is the keypress cycle on this scale: highest first, then back to
// unset. A tier off the scale steps from where Clamp puts it.
//
// Unset is in the ring rather than on a key of its own because a cycle that
// cannot reach unset makes a mistyped tier permanent.
func (s Scale) Next(t Tier) Tier {
	tiers := s.Tiers()
	t = s.Clamp(t)
	for i, tier := range tiers {
		if tier == t {
			if i+1 < len(tiers) {
				return tiers[i+1]
			}
			return Unset
		}
	}
	return tiers[0]
}

// Label is the scale as the settings row shows it, e.g. "+2 / -1".
func (s Scale) Label() string {
	s = s.Normalize()
	return fmt.Sprintf("+%d / -%d", s.Raised, s.Lowered)
}

func atLevel(level int) Tier {
	for _, tier := range Order {
		if tier.Level() == level {
			return tier
		}
	}
	return Unset
}

// Rank is the sort key. An unknown value ranks with the default rather than
// sorting first, so a tier that reaches here from a hand-edited goal file
// cannot jump the queue by being misspelled.
func (t Tier) Rank() int {
	if rank, ok := ranks[t]; ok {
		return rank
	}
	return ranks[Unset]
}

// Valid reports whether t is one of the tiers, or a deliberate unset.
func (t Tier) Valid() bool {
	_, ok := ranks[t]
	return ok
}

// Glyph uses geometric marks because emoji widths vary between terminals.
func (t Tier) Glyph() string {
	switch t {
	case Urgent:
		return "▲▲▲"
	case High:
		return "▲▲"
	case Medium:
		return "▲"
	case Low:
		return "▼"
	case Lower:
		return "▼▼"
	case Lowest:
		return "▼▼▼"
	}
	return ""
}

// Label is the tier's name for help text and errors.
func (t Tier) Label() string {
	if t == Unset {
		return "none"
	}
	return string(t)
}

// Better keeps the higher stated tier; an unset tier is no statement.
func Better(a, b Tier) Tier {
	if a == Unset {
		return b
	}
	if b == Unset {
		return a
	}
	if a.Rank() != b.Rank() {
		if a.Rank() < b.Rank() {
			return a
		}
		return b
	}
	return a
}

// Parse reads a tier written by a person: a goal's frontmatter, a command
// argument, a declaration payload. Case and surrounding space are the
// writer's business, not the scale's.
//
// "none", "clear" and "unset" are all a deliberate Unset, and so is the
// empty string -- a `priority:` key with nothing after it is somebody
// clearing the tier, not somebody misspelling one. Anything else is
// rejected rather than rounded to the default, so a typo is a message
// instead of a silently ordinary session.
func Parse(s string) (Tier, bool) {
	switch text := strings.ToLower(strings.TrimSpace(s)); text {
	case "", "none", "clear", "unset", "0":
		return Unset, true
	case "3", "+3":
		return Urgent, true
	case "2", "+2":
		return High, true
	case "1", "+1":
		return Medium, true
	case "-1":
		return Low, true
	case "-2":
		return Lower, true
	case "-3":
		return Lowest, true
	case "urgent", "high", "medium", "low", "lower", "lowest":
		return Tier(text), true
	default:
		return Unset, false
	}
}
