package tracing

import (
	"testing"
	"time"
)

// What tracing costs the board when nobody is reading is the number that
// decides whether it can be on by default, and it has to be measured rather
// than asserted. The box's own load moves an end-to-end latency figure by more
// than this effect, so it is pinned here where nothing else is running.
func BenchmarkRecordWithTracingOff(b *testing.B) {
	live.Store(nil)
	start := time.Now()
	end := start.Add(time.Millisecond)
	b.ReportAllocs()
	for b.Loop() {
		Record("tmux.capture-pane", start, end, nil, Attr{Key: "args", Value: 4})
	}
}

// The ordinary case with tracing on: the span is built, the sampler drops it,
// and nothing is queued, encoded or sent.
func BenchmarkRecordSampledAway(b *testing.B) {
	b.Setenv(SampleEnv, "1000000")
	tracer := &Tracer{queue: make(chan []Span, queueDepth), done: make(chan struct{})}
	live.Store(tracer)
	b.Cleanup(func() { live.Store(nil) })
	start := time.Now()
	end := start.Add(time.Millisecond)
	b.ReportAllocs()
	for b.Loop() {
		Record("tmux.capture-pane", start, end, nil, Attr{Key: "args", Value: 4})
	}
}

// A pass trace, built from measurements the pass already took, then dropped.
func BenchmarkPassTraceSampledAway(b *testing.B) {
	b.Setenv(SampleEnv, "1000000")
	tracer := &Tracer{queue: make(chan []Span, queueDepth), done: make(chan struct{})}
	live.Store(tracer)
	b.Cleanup(func() { live.Store(nil) })
	start := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		trace := NewTrace("poll.pass", start, start.Add(20*time.Millisecond),
			Attr{Key: "sessions", Value: 80}, Attr{Key: "live", Value: 80})
		at := start
		for _, d := range []time.Duration{0, time.Millisecond, 2 * time.Millisecond, 6 * time.Millisecond} {
			trace.Child("phase", at, at.Add(d))
			at = at.Add(d)
		}
		trace.Emit()
	}
}

// The kept case, which is what ids are actually paid for.
func BenchmarkPassTraceKept(b *testing.B) {
	b.Setenv(SampleEnv, "1")
	tracer := &Tracer{queue: make(chan []Span, queueDepth), done: make(chan struct{})}
	live.Store(tracer)
	b.Cleanup(func() { live.Store(nil) })
	go func() {
		for range tracer.queue {
		}
	}()
	start := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		trace := NewTrace("poll.pass", start, start.Add(20*time.Millisecond))
		at := start
		for _, d := range []time.Duration{0, time.Millisecond, 2 * time.Millisecond, 6 * time.Millisecond} {
			trace.Child("phase", at, at.Add(d))
			at = at.Add(d)
		}
		trace.Emit()
	}
}
