package extensionhost

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/tracetest"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// A board extension's span reaches the board's exporter named under and
// tagged with its id, carrying its attributes, failed by its error, and
// recorded once however often it is ended.
func TestAnExtensionsSpanReachesTheBoardsTrace(t *testing.T) {
	read := tracetest.Capture(t)
	board := &boardView{owner: "batch"}
	if !board.Tracer().Enabled() {
		t.Fatal("the tracer says it is off while the board is tracing")
	}
	span := board.Tracer().Start("store.read", slog.Int("rows", 3))
	failure := errors.New("locked")
	span.End(failure, slog.Duration("waited", 5*time.Millisecond), slog.Any("ids", []string{"a"}))
	span.End(nil)

	got := tracetest.One(t, read(), "batch.store.read")
	if !got.Failed {
		t.Error("the span did not record its error")
	}
	for key, want := range map[string]any{"extension": "batch", "rows": int64(3), "waited": 5.0, "ids": "[a]"} {
		if got.Attr(key) != want {
			t.Errorf("attr %s = %#v, want %#v (all %v)", key, got.Attr(key), want, got.Attrs)
		}
	}
}

// With tracing off nothing is timed or recorded.
func TestAnExtensionsTracerIsANoOpWhenTheBoardIsNotTracing(t *testing.T) {
	if tracing.Enabled() {
		t.Skip("another test left tracing on")
	}
	tr := (&Host{}).Tracer()
	if tr.Enabled() {
		t.Fatal("the tracer says it is on with tracing off")
	}
	if _, ok := tr.Start("store.read").(noSpan); !ok {
		t.Fatal("a span was built with tracing off")
	}
}

// Spans an extension timed itself arrive with their own start and end, in
// the trace and under the parent it named, with links to other traces; ids
// it chose come back as given, and one it left blank is minted and returned.
func TestAnExtensionRecordsPlacedSpans(t *testing.T) {
	read := tracetest.Capture(t)
	board := &boardView{owner: "batch"}
	recorder, ok := board.Tracer().(extension.SpanRecorder)
	if !ok {
		t.Fatal("the board's tracer does not record placed spans")
	}
	const trace, root = "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7"
	other := extension.SpanContext{TraceID: "0af7651916cd43dd8448eb211c80319c", SpanID: "b7ad6b7169203331"}
	start := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	ctxs, kept := recorder.Record(
		extension.SpanRecord{Name: "skill.run", Start: start, End: start.Add(time.Minute), TraceID: trace, SpanID: root,
			Links: []extension.SpanContext{other, {TraceID: "not-hex", SpanID: "nope"}}, Attrs: []slog.Attr{slog.String("skill", "x")}},
		extension.SpanRecord{Name: "tool.call", Start: start.Add(time.Second), End: start.Add(3 * time.Second), ParentID: root,
			Err: errors.New("exit 1")},
	)
	if !kept || len(ctxs) != 2 {
		t.Fatalf("kept=%v contexts=%v", kept, ctxs)
	}
	if ctxs[0] != (extension.SpanContext{TraceID: trace, SpanID: root}) {
		t.Errorf("the caller's ids came back as %v", ctxs[0])
	}
	if ctxs[1].TraceID != trace || !ctxs[1].Valid() || ctxs[1].SpanID == root {
		t.Errorf("a blank span id should be minted in the first span's trace: %v", ctxs[1])
	}

	spans := read()
	run, call := tracetest.One(t, spans, "batch.skill.run"), tracetest.One(t, spans, "batch.tool.call")
	if run.Trace != trace || run.ID != root || run.Parent != "" || !run.Start.Equal(start) || !run.End.Equal(start.Add(time.Minute)) {
		t.Errorf("root span %+v", run)
	}
	if len(run.Links) != 1 || run.Links[0] != other.TraceID+"/"+other.SpanID {
		t.Errorf("links %v: want the one well-formed link", run.Links)
	}
	if run.Attr("extension") != "batch" || run.Attr("skill") != "x" {
		t.Errorf("root attrs %v", run.Attrs)
	}
	if call.Trace != trace || call.Parent != root || call.ID != ctxs[1].SpanID || !call.Failed || !run.Brackets(call) {
		t.Errorf("child span %+v", call)
	}
}

// With tracing off a placed span is not recorded and no ids are made up.
func TestPlacedSpansAreANoOpWhenTheBoardIsNotTracing(t *testing.T) {
	if tracing.Enabled() {
		t.Skip("another test left tracing on")
	}
	ctxs, kept := (&Host{}).Tracer().(extension.SpanRecorder).Record(extension.SpanRecord{Name: "x"})
	if kept || ctxs != nil {
		t.Fatalf("kept=%v contexts=%v with tracing off", kept, ctxs)
	}
}
