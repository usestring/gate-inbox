package extension

import (
	"log/slog"
	"time"

	"github.com/usestring/gate-inbox/internal/tracing"
)

// Tracer opens spans in the board's own trace, sent wherever the operator
// pointed the board's tracing and sampled as the board's spans are. It sends
// nothing anywhere the board is not tracing, and the extension never chooses
// a destination of its own.
//
// Each span's name is prefixed with the extension's id and a dot, and the
// span carries the id as its extension attribute, so an extension's spans
// are told apart from the board's and from each other's in a shared dataset.
//
// Attributes name what a span acted on, never what was said to it: a span
// leaves the machine, and the board scrubs nothing from one.
type Tracer interface {
	// Enabled reports whether spans are being recorded, so a caller can
	// skip building attributes nobody will read.
	Enabled() bool
	// Start opens a span named name, timed from the call. Nothing is
	// recorded until End.
	Start(name string, attrs ...slog.Attr) TraceSpan
}

// TraceSpan is one timed operation an extension opened.
type TraceSpan interface {
	// End closes the span at the time of the call, adding attrs, and marks
	// it failed when err is not nil. Only the first End counts.
	End(err error, attrs ...slog.Attr)
}

// SpanRecorder is implemented by a Tracer that also takes spans the
// extension has already timed and placed: work it learned of after the
// fact, such as a tool call read from a transcript, whose real start and end
// are known, which belongs under another span, and whose ids the extension
// wants to store beside its own records so one can be found from the other.
// The board's Tracer implements it; assert for it, or use RecordSpans, which
// falls back to Start and End on a Tracer that does not.
type SpanRecorder interface {
	// Record sends spans as one unit: the sampler keeps or drops them
	// together, as it does a poll pass and its phases. It returns each
	// span's context, in order, and whether they were kept. A span the
	// caller gave ids keeps them, so they are known whether or not the
	// spans were kept; ids the board minted are returned only when they
	// were kept, since a dropped span has none.
	Record(spans ...SpanRecord) ([]SpanContext, bool)
}

// SpanContext names one span: OTLP's lowercase hex ids, 32 characters for
// the trace and 16 for the span.
type SpanContext struct {
	TraceID string
	SpanID  string
}

// Valid reports whether both ids are well-formed and not all zeros, which
// OTLP reserves for "no id".
func (c SpanContext) Valid() bool {
	return validHexID(c.TraceID, 32) && validHexID(c.SpanID, 16)
}

func validHexID(id string, n int) bool {
	if len(id) != n {
		return false
	}
	zero := true
	for _, r := range id {
		switch {
		case r == '0':
		case r >= '1' && r <= '9', r >= 'a' && r <= 'f':
			zero = false
		default:
			return false
		}
	}
	return !zero
}

// SpanRecord is one finished span for SpanRecorder.Record.
type SpanRecord struct {
	Name       string
	Start, End time.Time
	// Err marks the span failed.
	Err   error
	Attrs []slog.Attr
	// TraceID places the span in a trace. Blank takes the first span's,
	// minting one for the first span when it is blank too. A caller that
	// derives its trace id from something it already has, such as a
	// session, can send one trace in several Records over time, and the
	// sampler decides the same way for every one of them.
	TraceID string
	// SpanID is the span's own id; blank is minted.
	SpanID string
	// ParentID is the span this one runs under, in the same trace; blank
	// makes it a root.
	ParentID string
	// Links point at spans of other traces this one follows from, such as
	// the session that spawned the one this span belongs to.
	Links []SpanContext
}

// RecordSpans records spans on t: through SpanRecorder where t has it, and
// otherwise as one Start and End each, which keeps their names, attributes
// and failures but loses their timing, ids and parents.
func RecordSpans(t Tracer, spans ...SpanRecord) ([]SpanContext, bool) {
	if t == nil || !t.Enabled() || len(spans) == 0 {
		return nil, false
	}
	if r, ok := t.(SpanRecorder); ok {
		return r.Record(spans...)
	}
	for _, s := range spans {
		t.Start(s.Name, s.Attrs...).End(s.Err)
	}
	return nil, false
}

// scopedTracer is a Tracer as the extension with id is lent it.
type scopedTracer struct {
	Tracer
	id string
}

func (t scopedTracer) Start(name string, attrs ...slog.Attr) TraceSpan {
	return t.Tracer.Start(t.id+"."+name, t.scope(attrs)...)
}

// Record scopes each span as Start does, and passes them to the wrapped
// Tracer's recorder; through a Tracer without one it degrades as
// RecordSpans does.
func (t scopedTracer) Record(spans ...SpanRecord) ([]SpanContext, bool) {
	scoped := make([]SpanRecord, len(spans))
	for i, s := range spans {
		s.Name = t.id + "." + s.Name
		s.Attrs = t.scope(s.Attrs)
		scoped[i] = s
	}
	return RecordSpans(t.Tracer, scoped...)
}

func (t scopedTracer) scope(attrs []slog.Attr) []slog.Attr {
	return append([]slog.Attr{slog.String("extension", t.id)}, attrs...)
}

// KeyedID is value's anonymous id: the first 16 hex characters of HMAC-SHA256
// under this install's key, in namespace. The key is kept in the board's
// config directory with mode 0600 and never sent, so the same value always
// gets the same id on one install while nobody holding the ids can compute
// one from a guess. The board keys its own spans' session ids under the
// "session" namespace, so an extension that does the same joins them. Empty
// stays empty.
func KeyedID(namespace, value string) string { return tracing.KeyedID(namespace, value) }
