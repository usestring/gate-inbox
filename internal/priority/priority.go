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

import "strings"

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

// Order is highest-first: the tier cycle, and the order a queue walks.
var Order = []Tier{Urgent, High, Medium, Low, Lower, Lowest}

// ranks are the sort keys, lower first. Unset is the neutral default.
var ranks = map[Tier]int{Urgent: 0, High: 1, Medium: 2, Unset: 3, Low: 4, Lower: 5, Lowest: 6}

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

// Next is the keypress cycle: highest first, then back to unset.
//
// Unset is in the ring rather than on a key of its own because a cycle that
// cannot reach unset makes a mistyped tier permanent.
func Next(t Tier) Tier {
	for i, tier := range Order {
		if tier == t {
			if i+1 < len(Order) {
				return Order[i+1]
			}
			return Unset
		}
	}
	return Order[0]
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
