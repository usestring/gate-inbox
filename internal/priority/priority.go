// Package priority is how much the work in a session matters, on the one
// scale the whole manager speaks.
//
// The board already knows what every session is doing; status answers that.
// It had no answer for which of eight sessions waiting on the operator to
// answer first, and the mark that used to serve -- a boolean "this one
// before the others" -- could not tell the release from the customer's fix
// once more than one row carried it.
//
// The vocabulary is Linear's deliberately. A goal, a ticket and a session
// are the same work seen from three places, and a second scale that only
// one of them speaks is a scale nobody can act on.
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
	// Unset is nobody having said. It is a distinct value from Medium
	// rather than a default spelled as one, because the display has to be
	// able to tell an inferred middle from a stated one even though they
	// sort together.
	Unset Tier = ""
)

// Order is highest-first: the tier cycle, and the order a queue walks.
var Order = []Tier{Urgent, High, Medium, Low}

// ranks are the sort keys, lower first.
//
// Unset ranks with Medium so that adopting tiers does not silently demote
// every session nobody has triaged. Sorts are stable, so an untiered row
// holds its place among the mediums instead of falling to the bottom.
var ranks = map[Tier]int{Urgent: 0, High: 1, Medium: 2, Low: 3, Unset: 2}

// Rank is the sort key. An unknown value ranks with the middle rather than
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

// Glyph is the one-column mark drawn beside a name on the board.
//
// Geometric, not emoji: every other mark this board draws is (see
// styles.go), a terminal gives emoji two columns and a phone gives them a
// face. The shapes rank by eye without a legend -- filled above hollow,
// pointing up above pointing down -- and none of them is a status dot,
// because a mark that looks like a state is a state. Unset draws nothing
// at all: no tier is not a tier.
func (t Tier) Glyph() string {
	switch t {
	case Urgent:
		return "▲"
	case High:
		return "△"
	case Medium:
		return "·"
	case Low:
		return "▽"
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

// Better is the higher of two tiers, for resolving a session's own tier
// against the group's.
//
// On a tie it keeps a, which callers pass as the nearer of the two, so a
// session's own stated Medium is not swapped for an inherited one. A tie
// between a stated tier and Unset takes the stated one: they sort
// together, and carrying the statement forward is what lets the display
// tell them apart.
func Better(a, b Tier) Tier {
	if a.Rank() != b.Rank() {
		if a.Rank() < b.Rank() {
			return a
		}
		return b
	}
	if a != Unset {
		return a
	}
	return b
}

// Parse reads a tier written by a person: a goal's frontmatter, a command
// argument, a declaration payload. Case and surrounding space are the
// writer's business, not the scale's.
//
// "none", "clear" and "unset" are all a deliberate Unset, and so is the
// empty string -- a `priority:` key with nothing after it is somebody
// clearing the tier, not somebody misspelling one. Anything else is
// rejected rather than rounded to the middle, so a typo is a message
// instead of a silently ordinary session.
func Parse(s string) (Tier, bool) {
	switch text := strings.ToLower(strings.TrimSpace(s)); text {
	case "", "none", "clear", "unset":
		return Unset, true
	case "urgent", "high", "medium", "low":
		return Tier(text), true
	default:
		return Unset, false
	}
}
