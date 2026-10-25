package scoring

import (
	"math"
	"slices"
	"time"
)

// The headroom score ranks subscription accounts for a launch. What is lost by
// leaving an account is the quota it resets before spending: weekly quota
// left at a reset is gone. The score is that headroom weighted by how soon
// the reset comes.
const TermHeadroom = "headroom"

// The windows a reading must carry. FiveHour is a rate cap that refills all
// day; SevenDay, and any other window, is weekly quota that expires.
const (
	FiveHour = "five_hour"
	SevenDay = "seven_day"
)

// ObservedFreshness is how old a reading may be at its source before the
// account counts as having none.
const ObservedFreshness = 2 * time.Minute

// MinHorizon floors the time to a reset the urgency divides by, so an account
// minutes from its reset scores high rather than infinite.
const MinHorizon = 15 * time.Minute

// SwitchMargin is how much better another account must score before a
// running session is moved onto it: a move costs a restart and the cache,
// and two accounts scoring alike would otherwise trade it back and forth.
const SwitchMargin = 1.5

// Usage is one account's quota as last observed.
type Usage struct {
	// ObservedAt is when the reading was taken at its source; zero is as
	// fresh as the call. A reading observed in the future, or more than
	// ObservedFreshness before it is used, makes the account ineligible.
	ObservedAt time.Time
	// Windows are the account's rate-limit windows by name. Scoring needs
	// FiveHour and SevenDay; any other window also counts.
	Windows map[string]Window
}

// Window is one rate-limit window.
type Window struct {
	// Utilization is the percentage used, 0 to 100; nil is unknown, which
	// makes the account ineligible.
	Utilization *float64
	// ResetsAt is when the window resets; zero only for an unused window.
	ResetsAt time.Time
}

// Standing is how one account stands for a launch now.
type Standing struct {
	FiveHourHeadroom float64
	// Usable says the account may take a launch: a fresh reading of every
	// window, none of them at its ceiling.
	Usable bool
	// Score is how urgently the account's quota must be spent for none of it
	// to expire unused: the total of HeadroomScore for the scoring window,
	// Headroom over the square of the hours to ResetsAt.
	Score float64
	// Headroom is the points left before the scoring window's ceiling: its
	// limit less the reserve kept at that point of the period.
	Headroom float64
	// Window names the scoring window -- the weekly window that scores
	// lowest, or the one that made the account unusable -- and ResetsAt is
	// when it resets; zero for an unused window or an unread account.
	Window   string
	ResetsAt time.Time
	// Margin is the least headroom any window has left, the five-hour one
	// included, and MarginWindow names it: how close the account is to the
	// point where it stops taking work.
	Margin       float64
	MarginWindow string
	// Reason says why an account is not usable: "unread", "stale",
	// "limit" or "reserve".
	Reason string
}

// Ceiling is the utilization a window may reach before the account stops
// taking launches: 100 less a reserve of 15% of the period's quota just
// after a reset, tapering to 5% just before the next one.
func Ceiling(untilReset, period time.Duration) float64 {
	return 95 - 10*untilReset.Seconds()/period.Seconds()
}

// Urgency is the weight on a window's headroom: one over the square of the
// hours to its reset, floored at MinHorizon. Squaring the hours makes the
// account whose unspent quota is closest to being lost win even over one
// with more headroom that resets much later: ten times nearer its reset, the
// same headroom scores a hundred times higher.
func Urgency(untilReset time.Duration) float64 {
	hours := max(untilReset, MinHorizon).Hours()
	return 1 / (hours * hours)
}

// HeadroomScore is one weekly window's score: its headroom, in points of
// utilization, weighted by the Urgency of its reset.
func HeadroomScore(headroom float64, untilReset time.Duration) Score {
	return Of(Term{Name: TermHeadroom, Weight: Urgency(untilReset), Value: headroom})
}

// WorthSwitching is whether a session on an account scoring from should move
// to one scoring to: only for a score SwitchMargin times higher, since a
// close score buys nothing a restart pays for.
func WorthSwitching(from, to float64) bool {
	return to >= from*SwitchMargin
}

// Headroom is how an account stands on a reading: its score, or why the
// reading cannot authorize a launch -- stale, missing a required window,
// unknown, expired, at its limit, or inside the reserve Ceiling keeps.
//
// Every window must be clear of its ceiling for the account to be usable,
// but only the weekly windows score: the five-hour window is a rate cap
// that refills all day, while weekly quota left at a reset is gone. With
// several weekly windows the lowest score counts, since the tightest one
// bounds what can still be spent.
func Headroom(u Usage, now time.Time) Standing {
	if !u.ObservedAt.IsZero() && (u.ObservedAt.After(now) || now.Sub(u.ObservedAt) > ObservedFreshness) {
		return Standing{Reason: "stale"}
	}
	if _, ok := u.Windows[FiveHour]; !ok {
		return Standing{Reason: "unread"}
	}
	if _, ok := u.Windows[SevenDay]; !ok {
		return Standing{Reason: "unread"}
	}
	names := make([]string, 0, len(u.Windows))
	for name := range u.Windows {
		names = append(names, name)
	}
	slices.Sort(names)
	out := Standing{Usable: true, Score: math.Inf(1), Margin: math.Inf(1)}
	var spent *Standing
	for _, name := range names {
		w := u.Windows[name]
		period := 7 * 24 * time.Hour
		if name == FiveHour {
			period = 5 * time.Hour
		}
		if w.Utilization == nil {
			return Standing{Reason: "unread", Window: name}
		}
		used := *w.Utilization
		if math.IsNaN(used) || used < 0 {
			return Standing{Reason: "unread", Window: name}
		}
		// An unused window has its whole period ahead of it.
		left := period
		if used != 0 || !w.ResetsAt.IsZero() {
			if !w.ResetsAt.After(now) {
				return Standing{Reason: "stale", Window: name}
			}
			left = min(w.ResetsAt.Sub(now), period)
		}
		ceiling := Ceiling(left, period)
		reason := ""
		switch {
		case used >= 100:
			reason = "limit"
		case used >= ceiling:
			reason = "reserve"
		}
		if reason != "" {
			// The window furthest from its reset is what keeps the account
			// shut longest.
			if spent == nil || w.ResetsAt.After(spent.ResetsAt) {
				spent = &Standing{Reason: reason, Window: name, ResetsAt: w.ResetsAt}
			}
			continue
		}
		head := ceiling - used
		if name == FiveHour {
			out.FiveHourHeadroom = head
		}
		if head < out.Margin {
			out.Margin, out.MarginWindow = head, name
		}
		if name == FiveHour {
			continue
		}
		if score := HeadroomScore(head, left).Total(); score < out.Score {
			out.Score, out.Headroom, out.Window, out.ResetsAt = score, head, name, w.ResetsAt
		}
	}
	if spent != nil {
		return *spent
	}
	return out
}
