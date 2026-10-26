// Package scoring ranks candidates by a sum of named, independently weighted
// terms, and keeps every term on the result so a caller can show why one
// candidate came before another.
//
// Every scorer here shares one principle: rank each candidate by what is lost
// if it is left, and how soon. Smart triage loses an operator's fresh context,
// a waiting agent's time and a long turn's head start; an account router loses
// quota that resets before it is spent. The terms are each one such loss, put
// on a common scale so they can be added.
package scoring

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Term is one named contribution to a score: a raw value on the scorer's
// common scale and the weight the caller gives it. A weight of 0 turns the
// term off without removing it from the breakdown.
type Term struct {
	Name   string
	Weight float64
	Value  float64
}

// Points is what the term adds to its score.
func (t Term) Points() float64 { return t.Weight * t.Value }

// Score is a candidate's terms. Its total is their sum, so a term can be
// added, reweighted or switched off without the others changing meaning.
type Score struct {
	Terms []Term
}

// Of collects terms into a score.
func Of(terms ...Term) Score { return Score{Terms: terms} }

// Total is the sum of every term's points.
func (s Score) Total() float64 {
	var total float64
	for _, t := range s.Terms {
		total += t.Points()
	}
	return total
}

// Term is the named term, false when the score has none by that name.
func (s Score) Term(name string) (Term, bool) {
	for _, t := range s.Terms {
		if t.Name == name {
			return t, true
		}
	}
	return Term{}, false
}

// String is the breakdown, the terms that add anything followed by the
// total: "wait 2520 + recency 1120 = 3640".
func (s Score) String() string {
	var parts []string
	for _, t := range s.Terms {
		if p := t.Points(); p != 0 {
			parts = append(parts, t.Name+" "+formatPoints(p))
		}
	}
	if len(parts) == 0 {
		return "0"
	}
	return strings.Join(parts, " + ") + " = " + formatPoints(s.Total())
}

func formatPoints(p float64) string {
	return strconv.FormatFloat(p, 'f', 0, 64)
}

// HalfLife is a loss that fades as it ages: 1 at age zero, halving every
// halfLife. It is absolute rather than a rank, so candidates that are all old
// all read near 0 and are indistinguishable on it. A negative age reads as
// zero; a non-positive halfLife is no decay at all, and reads 0.
func HalfLife(age, halfLife time.Duration) float64 {
	if halfLife <= 0 {
		return 0
	}
	return math.Exp2(-max(age, 0).Seconds() / halfLife.Seconds())
}

// Saturating is a quantity's share of a reference amount, clamped to [0, 1]:
// a turn expected to run as long as the reference or longer reads 1. A
// non-positive reference reads 0.
func Saturating(amount, reference time.Duration) float64 {
	if reference <= 0 {
		return 0
	}
	return min(max(amount.Seconds()/reference.Seconds(), 0), 1)
}
