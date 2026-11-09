package tracing

import (
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"
)

// Tracing is on by default, which is only defensible because almost nothing is
// sent. A board that traced every pass and every tmux call would put roughly
// seven spans a second on the wire from every operator's machine, all day, to
// answer questions nobody is asking about the passes that went fine.
//
// So the decision is made at the end, when the outcome is known -- tail
// sampling. What survives is what somebody would actually go looking for: a
// failure, or something slow enough to have been felt. Everything else is kept
// at a low fixed rate, because a dataset holding only the bad ones cannot say
// what normal is, and "slow" has no meaning without it.
const (
	// SampleEnv overrides how much of the ordinary traffic is kept, as
	// one-in-N. Setting it to 1 keeps everything, which is what a deliberate
	// soak wants and what a shared board should never be left on.
	SampleEnv = "GATE_INBOX_TRACE_SAMPLE"

	// baselineRate is that N. A pass happens about twice a second, so ten
	// keeps one ordinary pass about every five seconds from every board:
	// enough that rates and percentiles hold up for a single board over a
	// short window, while still dropping 90% of the spans that would only
	// have confirmed things were fine.
	baselineRate = 10

	// slowEnough is the line above which a span is kept whatever the sampler
	// says. A quarter of a second is about where a person stops experiencing
	// a keystroke as instant, which is the thing this exists to catch.
	slowEnough = 250 * time.Millisecond
)

// keep decides whether these spans are worth the wire, and says why.
//
// The reason travels with them: a dataset that has dropped 90% of its ordinary
// traffic will otherwise be counted as if it were all of it, and every rate
// read off it will be wrong by a factor of ten. Summing sample.rate rather
// than counting rows is what makes the arithmetic come out.
//
// Every span is looked at, not only the root: a pass that was quick overall
// can hold the one slow tmux call somebody is looking for, and keeping the
// whole trace brings its context along.
func keep(spans []Span) (bool, []Attr) {
	if len(spans) > 0 && strings.HasPrefix(spans[0].Name, "account.") {
		return true, []Attr{{Key: "sample.reason", Value: "accounting"}, {Key: "sample.rate", Value: 1}}
	}
	for _, span := range spans {
		if span.Err != nil {
			return true, []Attr{{Key: "sample.reason", Value: "error"}, {Key: "sample.rate", Value: 1}}
		}
	}
	for _, span := range spans {
		if span.End.Sub(span.Start) >= slowEnough {
			return true, []Attr{{Key: "sample.reason", Value: "slow"}, {Key: "sample.rate", Value: 1}}
		}
	}
	rate := baselineSample()
	if rate <= 1 {
		return true, []Attr{{Key: "sample.reason", Value: "all"}, {Key: "sample.rate", Value: 1}}
	}
	if rand.IntN(rate) != 0 {
		return false, nil
	}
	return true, []Attr{{Key: "sample.reason", Value: "baseline"}, {Key: "sample.rate", Value: rate}}
}

// baselineSample reads the override once per call rather than caching it, which
// costs one map lookup on a path that is already about to marshal JSON, and
// means an operator can change it without arguing with a running board.
func baselineSample() int {
	raw := os.Getenv(SampleEnv)
	if raw == "" {
		return baselineRate
	}
	rate, err := strconv.Atoi(raw)
	if err != nil || rate < 1 {
		return baselineRate
	}
	return rate
}
