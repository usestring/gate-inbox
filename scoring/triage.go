package scoring

import "time"

// Smart triage's terms. Every term is in points, and a point is one second of
// a finished session's wait: the wait term at state weight 1. Putting the
// bonuses on that scale is what lets them be read against the wait they are
// added to -- a bonus of 1200 points is worth what twenty more minutes of
// waiting would have been to a finished session.
const (
	TermWait    = "wait"
	TermRecency = "recency"
	TermHorizon = "horizon"
	TermCache   = "cache"
)

// WaitFloor is added to every wait so rows that have only just come in do not
// all score zero and lose their state weights.
const WaitFloor = time.Minute

// The recency term rewards a session the operator touched moments ago: their
// context on it is fresh, so they can advance it in seconds where a cold
// session costs them a re-read. That context is what is lost by leaving it,
// and it goes fast.
//
// RecencyHalfLife is five minutes because that is about how long a thread
// stays in the operator's head while they are draining other sessions: two
// or three handovers later it has to be re-read. RecencyScale is twenty
// minutes, so a session touched 30 seconds ago (+1120) goes ahead of a
// question that has waited eight minutes (2 x 9m = 1080), while one touched
// a quarter of an hour ago (+150) barely moves, and one touched an hour ago
// (+0.3) or two days ago (+0) reads the same as one never touched at all.
const (
	RecencyHalfLife = 5 * time.Minute
	RecencyScale    = 20 * time.Minute
)

// The horizon term hands a session whose next turn will run long over
// earlier, so that turn gets started while the operator works through the
// short ones; what is lost by leaving it is the head start. The term reads an
// expected turn duration and saturates at HorizonReference, so a turn
// expected to run half an hour or more earns all of HorizonScale and shorter
// turns earn a share of it.
//
// Ten minutes of scale lets a long turn's session overtake a finished
// session that has waited up to ten minutes longer, without a long turn ever
// jumping a question that has been stalled for an hour.
const (
	HorizonReference = 30 * time.Minute
	HorizonScale     = 10 * time.Minute
)

// TriageInput is what smart triage knows about one candidate.
type TriageInput struct {
	// Wait is how long since a person last gave the session a turn, or since
	// it was created when nobody has.
	Wait time.Duration
	// StateWeight weighs the wait by the session's state: a question
	// outweighs a finish.
	StateWeight float64
	// SinceOperator is how long since the operator's last turn on the
	// session, read only when Touched.
	SinceOperator time.Duration
	// Touched is whether the operator has ever given the session a turn. A
	// session never touched has no fresh context to lose.
	Touched bool
	// ExpectedTurn is how long the session's next turn is expected to run,
	// zero when nothing says.
	ExpectedTurn time.Duration
	// CacheStake is what an extension says the session's prompt cache
	// expiring before its next turn would cost, in dollars. The cache term
	// does not read it yet. See Triage.
	CacheStake float64
}

// TriageWeights are the multipliers on each term. A weight of 1 is the term at
// the scale its constants give it; 0 turns it off.
type TriageWeights struct {
	Wait, Recency, Horizon, Cache float64
}

// DefaultTriageWeights is every term at its own scale, bar the cache-expiry
// term, which is off until it reads anything.
func DefaultTriageWeights() TriageWeights {
	return TriageWeights{Wait: 1, Recency: 1, Horizon: 1}
}

// Triage scores one candidate. A higher total is handed over first.
func Triage(in TriageInput, w TriageWeights) Score {
	recency := 0.0
	if in.Touched {
		recency = RecencyScale.Seconds() * HalfLife(in.SinceOperator, RecencyHalfLife)
	}
	return Of(
		Term{Name: TermWait, Weight: w.Wait, Value: (max(in.Wait, 0) + WaitFloor).Seconds() * in.StateWeight},
		Term{Name: TermRecency, Weight: w.Recency, Value: recency},
		Term{Name: TermHorizon, Weight: w.Horizon, Value: HorizonScale.Seconds() * Saturating(in.ExpectedTurn, HorizonReference)},
		// The cache-expiry term's slot: what a session loses when its prompt
		// cache expires before its next turn. It reads nothing yet, and ships
		// with weight 0. Until it does, an extension that publishes a cache
		// expiry as a queue deadline puts the session ahead of every score in
		// its tier, an unbounded bonus; this term is where a bounded one
		// replaces that, read from CacheStake and capped, after the state
		// weight like the other bonuses.
		Term{Name: TermCache, Weight: w.Cache},
	)
}
