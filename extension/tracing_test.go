package extension

import (
	"errors"
	"log/slog"
	"testing"
	"time"
)

type startOnly struct{ started []string }

func (t *startOnly) Enabled() bool { return true }
func (t *startOnly) Start(name string, _ ...slog.Attr) TraceSpan {
	t.started = append(t.started, name)
	return endless{}
}

type endless struct{}

func (endless) End(error, ...slog.Attr) {}

type recorder struct {
	startOnly
	got []SpanRecord
}

func (r *recorder) Record(spans ...SpanRecord) ([]SpanContext, bool) {
	r.got = append(r.got, spans...)
	out := make([]SpanContext, len(spans))
	for i, s := range spans {
		out[i] = SpanContext{TraceID: s.TraceID, SpanID: s.SpanID}
	}
	return out, true
}

// A scoped tracer names and tags placed spans as it does started ones, and
// passes them through whole.
func TestAScopedTracerScopesPlacedSpans(t *testing.T) {
	inner := &recorder{}
	scoped := scopedTracer{Tracer: inner, id: "batch"}
	at := time.Now()
	_, kept := RecordSpans(scoped, SpanRecord{Name: "tool.call", Start: at, End: at.Add(time.Second), ParentID: "00f067aa0ba902b7"})
	if !kept || len(inner.got) != 1 {
		t.Fatalf("kept=%v got=%v", kept, inner.got)
	}
	got := inner.got[0]
	if got.Name != "batch.tool.call" || got.Attrs[0].Key != "extension" || got.Attrs[0].Value.String() != "batch" ||
		got.ParentID != "00f067aa0ba902b7" || !got.Start.Equal(at) {
		t.Errorf("scoped span %+v", got)
	}
}

// A Tracer that cannot take placed spans still gets them as started ones,
// so an extension written against the recorder works on any Tracer.
func TestPlacedSpansFallBackToStartAndEnd(t *testing.T) {
	inner := &startOnly{}
	ctxs, kept := RecordSpans(scopedTracer{Tracer: inner, id: "batch"},
		SpanRecord{Name: "a"}, SpanRecord{Name: "b", Err: errors.New("x")})
	if kept || ctxs != nil || len(inner.started) != 2 || inner.started[0] != "batch.a" {
		t.Fatalf("kept=%v ctxs=%v started=%v", kept, ctxs, inner.started)
	}
}

func TestSpanContextValidity(t *testing.T) {
	for ctx, want := range map[SpanContext]bool{
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7"}: true,
		{TraceID: "00000000000000000000000000000000", SpanID: "00f067aa0ba902b7"}: false,
		{TraceID: "4BF92F3577B34DA6A3CE929D0E0E4736", SpanID: "00f067aa0ba902b7"}: false,
		{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902"}:   false,
	} {
		if ctx.Valid() != want {
			t.Errorf("%+v valid=%v, want %v", ctx, !want, want)
		}
	}
}
