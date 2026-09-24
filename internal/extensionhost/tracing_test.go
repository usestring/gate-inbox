package extensionhost

import (
	"errors"
	"log/slog"
	"testing"
	"time"

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
