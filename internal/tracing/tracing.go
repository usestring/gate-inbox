// Package tracing records where a poll pass and the tmux calls around it
// spend their time, as spans rather than as a log line.
//
// The manager already logs a pass's phase timings, and that log is what every
// performance question about this program has had to be answered from. It
// cannot answer the ones that matter: a phase total says nothing about which
// of the forty tmux calls underneath it was slow, two phases on two different
// passes cannot be laid beside each other, and nothing correlates any of it
// with what the rest of the box was doing. Spans carry the same numbers with
// a shape and a clock, so the question "what is taking what" is a query
// instead of an afternoon.
//
// Nothing here runs unless the operator asks for it. Enabled is one atomic
// load, and every call site is guarded by it, so the default build pays a
// branch per pass and nothing else.
package tracing

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"time"
)

// Env is the switch, and it defaults to off. A board nobody configured sends
// nothing anywhere: where traces go is the operator's to say, and a build for
// one estate says it for that estate's operators.
//
// Once on, almost nothing is sent -- see sample.go -- and nothing it does can
// slow the board down: the recording path is one atomic load, the credential
// is fetched off the startup path, and a destination that stops answering
// costs dropped spans rather than a wait.
//
//	GATE_INBOX_TRACES unset                        the default: nothing is recorded
//	GATE_INBOX_TRACES=off                          the default, stated explicitly
//	GATE_INBOX_TRACES=file:/tmp/board.otlp.jsonl   one OTLP payload per line
//	GATE_INBOX_TRACES=axiom                        sampled, to Axiom
//
// The Axiom destination needs GATE_INBOX_TRACE_DATASET named, and a token:
// AXIOM_TRACE_TOKEN, or a Secret Manager secret named by
// GATE_INBOX_TRACE_SECRET and GATE_INBOX_TRACE_PROJECT -- see secret.go.
const Env = "GATE_INBOX_TRACES"

// ServiceName is how these spans are told apart from every other service's in
// a shared traces dataset, which is how the estate is already organised: one
// dataset, separated by service.name.
const ServiceName = "gate-inbox"

// live is read on the hot path by Enabled and written once at startup.
var live atomic.Pointer[Tracer]

// Enabled reports whether anything is recording. Callers building spans must
// ask first: assembling a pass's spans allocates, and the overwhelmingly
// common case is that nobody is listening.
func Enabled() bool { return live.Load() != nil }

// Attr is one span attribute. Only the types OTLP has a JSON encoding for
// here are accepted; anything else is recorded as its %v, which is better
// than dropping the attribute and far better than panicking inside a poll.
type Attr struct {
	Key   string
	Value any
}

// Span is one timed thing. Children carry the parent's trace so a pass and
// its phases arrive as one waterfall.
type Span struct {
	Name     string
	Start    time.Time
	End      time.Time
	Attrs    []Attr
	Err      error
	traceID  string
	spanID   string
	parentID string
}

// Trace is a root span and the children collected under it, emitted
// together. Building one off to the side rather than opening spans in place
// keeps the pass's control flow exactly as it was: the phases are already
// measured, and this only gives the measurements a shape.
type Trace struct {
	root     Span
	children []Span
}

// NewTrace opens a trace whose root covers start to end.
func NewTrace(name string, start, end time.Time, attrs ...Attr) *Trace {
	return &Trace{root: Span{Name: name, Start: start, End: end, Attrs: attrs}}
}

// Child adds a span under the root. The caller supplies real timestamps:
// a phase whose duration is accumulated out of several disjoint stretches
// has no honest start and end, and belongs on its parent as an attribute
// rather than as a child span with invented ones.
func (t *Trace) Child(name string, start, end time.Time, attrs ...Attr) {
	t.children = append(t.children, Span{Name: name, Start: start, End: end, Attrs: attrs})
}

// Emit hands the trace to the exporter. It never blocks: a full buffer drops
// the trace and counts it. Telemetry that can stall the thing it measures is
// worse than no telemetry, and this program's whole problem is stalls.
func (t *Trace) Emit() {
	tracer := live.Load()
	if tracer == nil {
		return
	}
	spans := make([]Span, 0, len(t.children)+1)
	spans = append(spans, t.root)
	spans = append(spans, t.children...)
	wanted, why := keep(spans)
	if !wanted {
		return
	}
	identify(spans)
	spans[0].Attrs = append(spans[0].Attrs, why...)
	tracer.enqueue(spans)
}

// Record emits a single span with no children, for work that stands alone --
// a tmux call belongs to whatever happened to be running, and guessing at
// that parent would invent a hierarchy rather than report one.
func Record(name string, start, end time.Time, err error, attrs ...Attr) {
	tracer := live.Load()
	if tracer == nil {
		return
	}
	spans := []Span{{Name: name, Start: start, End: end, Attrs: attrs, Err: err}}
	wanted, why := keep(spans)
	if !wanted {
		return
	}
	identify(spans)
	spans[0].Attrs = append(spans[0].Attrs, why...)
	tracer.enqueue(spans)
}

// identify hands out ids, after the sampler has decided the spans are worth
// sending. Minting them earlier meant two reads of crypto/rand for every span
// the sampler was about to throw away, which measured at 920ns against the
// 150ns it costs to build and drop one without them -- all of it spent on a
// span nobody would ever read.
func identify(spans []Span) {
	trace, root := newTraceID(), newSpanID()
	spans[0].traceID, spans[0].spanID = trace, root
	for i := 1; i < len(spans); i++ {
		spans[i].traceID, spans[i].spanID, spans[i].parentID = trace, newSpanID(), root
	}
}

// Ids are random, not cryptographic. A profile of the instrumented board put
// 88% of a span's cost in here and 82% of that inside crypto/rand -- paid on
// the event loop, to make a correlation id unguessable. Nothing guards
// anything by these: they exist so two spans of the same trace can be joined,
// and 64 and 128 bits of ordinary randomness collide no more often for that
// than 64 and 128 bits of the expensive kind.
func newSpanID() string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], rand.Uint64())
	return hex.EncodeToString(b[:])
}

func newTraceID() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:8], rand.Uint64())
	binary.BigEndian.PutUint64(b[8:16], rand.Uint64())
	return hex.EncodeToString(b[:])
}

// Dropped is how many traces the exporter could not keep up with. A
// measurement that silently loses some of its samples reports a board that
// is faster than it is, so this is surfaced rather than hidden.
func Dropped() int64 {
	if tracer := live.Load(); tracer != nil {
		return tracer.dropped.Load()
	}
	return 0
}

// Resolve turns the environment variable's value into a destination.
func Resolve(spec string) (sink string, target string, err error) {
	spec = strings.TrimSpace(spec)
	switch {
	case spec == "" || spec == "off" || spec == "false" || spec == "0" || spec == "none":
		return "", "", nil
	case spec == "axiom":
		return "axiom", "", nil
	case strings.HasPrefix(spec, "file:"):
		path := strings.TrimPrefix(spec, "file:")
		if path == "" {
			return "", "", fmt.Errorf("%s=file: needs a path", Env)
		}
		return "file", path, nil
	default:
		return "", "", fmt.Errorf("%s=%q: want \"axiom\", \"file:<path>\" or \"off\"", Env, spec)
	}
}
