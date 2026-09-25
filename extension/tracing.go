package extension

import (
	"log/slog"
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

// scopedTracer is a Tracer as the extension with id is lent it.
type scopedTracer struct {
	Tracer
	id string
}

func (t scopedTracer) Start(name string, attrs ...slog.Attr) TraceSpan {
	return t.Tracer.Start(t.id+"."+name, append([]slog.Attr{slog.String("extension", t.id)}, attrs...)...)
}
