package extensionhost

import (
	"log/slog"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// tracer is the board's tracing as an extension is lent it. With scope set
// it names and tags each span with that extension's id; unscoped, the
// registry does, since one Host is shared by every extension of a session.
type tracer struct{ scope string }

func (t tracer) Enabled() bool { return tracing.Enabled() }

func (t tracer) Start(name string, attrs ...slog.Attr) extension.TraceSpan {
	if !tracing.Enabled() {
		return noSpan{}
	}
	if t.scope != "" {
		name = t.scope + "." + name
		attrs = append([]slog.Attr{slog.String("extension", t.scope)}, attrs...)
	}
	return &span{name: name, start: time.Now(), attrs: attrs}
}

// Record is extension.SpanRecorder: spans the extension already timed and
// placed, scoped like Start's and sampled as one unit.
func (t tracer) Record(spans ...extension.SpanRecord) ([]extension.SpanContext, bool) {
	if !tracing.Enabled() || len(spans) == 0 {
		return nil, false
	}
	placed := make([]tracing.Placed, len(spans))
	for i, s := range spans {
		name, attrs := s.Name, s.Attrs
		if t.scope != "" {
			name = t.scope + "." + name
			attrs = append([]slog.Attr{slog.String("extension", t.scope)}, attrs...)
		}
		links := make([]tracing.Link, 0, len(s.Links))
		for _, l := range s.Links {
			links = append(links, tracing.Link{TraceID: l.TraceID, SpanID: l.SpanID})
		}
		placed[i] = tracing.Placed{
			Span:    tracing.Span{Name: name, Start: s.Start, End: s.End, Err: s.Err, Attrs: convertAttrs(attrs)},
			TraceID: s.TraceID, SpanID: s.SpanID, ParentID: s.ParentID, Links: links,
		}
	}
	kept, placed := tracing.RecordPlaced(placed)
	out := make([]extension.SpanContext, len(placed))
	for i, p := range placed {
		out[i] = extension.SpanContext{TraceID: p.TraceID, SpanID: p.SpanID}
	}
	return out, kept
}

func convertAttrs(attrs []slog.Attr) []tracing.Attr {
	out := make([]tracing.Attr, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, tracing.Attr{Key: attr.Key, Value: attrValue(attr.Value.Resolve())})
	}
	return out
}

type noSpan struct{}

func (noSpan) End(error, ...slog.Attr) {}

type span struct {
	name  string
	start time.Time
	attrs []slog.Attr
	once  sync.Once
}

func (s *span) End(err error, attrs ...slog.Attr) {
	s.once.Do(func() {
		tracing.Record(s.name, s.start, time.Now(), err, convertAttrs(append(s.attrs, attrs...))...)
	})
}

// attrValue is an attribute's value as the exporter encodes one: the kinds
// it has an encoding for as themselves, anything else as its text.
func attrValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindBool:
		return v.Bool()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindDuration:
		return v.Duration()
	}
	return v.String()
}
