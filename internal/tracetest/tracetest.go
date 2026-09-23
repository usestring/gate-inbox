// Package tracetest reads back the spans a package under test emitted.
//
// A test that asserts on instrumentation has to see what actually left the
// program, not what the call site handed the tracer: the whole risk in a span
// is that it carries the wrong name, the wrong attribute, or -- the one that
// matters most here -- something that was never meant to leave the machine.
// So this captures through the file exporter and decodes the OTLP the same
// way a reader of the trace file would, rather than intercepting anything.
//
// It lives beside the packages it serves instead of inside tracing because
// tracing is what it is testing through, and a test helper that shares the
// exporter's internals cannot catch the exporter changing under it.
package tracetest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tracing"
)

// Span is one recorded span, flattened to what an assertion asks about.
type Span struct {
	Name   string
	Trace  string
	Parent string
	Attrs  map[string]any
	// Start and End are the window the span claims to have measured. They are
	// here because the commonest way to get instrumentation wrong is to build
	// the span where it is emitted rather than where the work began, which
	// produces a perfectly well-formed span of no duration.
	Start, End time.Time
	// Failed is the OTLP error status, which is how a span says the thing it
	// measured did not work.
	Failed bool
}

// Brackets reports whether this span covers the other one whole, which is what
// an outer command has to do to the work it contains.
func (s Span) Brackets(inner Span) bool {
	return !s.Start.After(inner.Start) && !s.End.Before(inner.End)
}

// Attr is one attribute, or nil when the span does not carry it. Returning
// the zero interface rather than a second boolean keeps the assertions
// readable: a missing attribute and a wrong one fail the same test.
func (s Span) Attr(key string) any { return s.Attrs[key] }

// Capture turns tracing on for the rest of the test and returns the func that
// reads back what was recorded. Calling it closes the exporter, so the spans
// are on disk before they are read: the exporter batches on its own clock and
// a test that did not wait for it would be flaky rather than wrong.
func Capture(t *testing.T) func() []Span {
	t.Helper()
	// Keep every span for the duration of the test. In a running manager the
	// sampler throws away all but a fiftieth of the ordinary traffic, which is
	// what makes tracing affordable to leave on -- but a test that emits one
	// span and asks whether it arrived would then pass or fail on a coin toss
	// weighted fifty to one against it.
	t.Setenv(tracing.SampleEnv, "1")
	path := filepath.Join(t.TempDir(), "spans.jsonl")
	tracer, err := tracing.Start("file:" + path)
	if err != nil {
		t.Fatalf("start tracing: %v", err)
	}
	if tracer == nil {
		t.Fatal("tracing did not start")
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			tracer.Close()
		}
	})
	return func() []Span {
		if !closed {
			tracer.Close()
			closed = true
		}
		return decode(t, path)
	}
}

// Named is every span with this name, in the order they were written.
func Named(spans []Span, name string) []Span {
	var found []Span
	for _, span := range spans {
		if span.Name == name {
			found = append(found, span)
		}
	}
	return found
}

// One is the single span with this name, and fails the test when there is not
// exactly one: an assertion that silently read the first of several would pass
// for instrumentation that fires on every loop iteration.
func One(t *testing.T, spans []Span, name string) Span {
	t.Helper()
	found := Named(spans, name)
	if len(found) != 1 {
		t.Fatalf("got %d %q spans, want 1 (recorded: %s)", len(found), name, strings.Join(names(spans), ", "))
	}
	return found[0]
}

func names(spans []Span) []string {
	out := make([]string, 0, len(spans))
	for _, span := range spans {
		out = append(out, span.Name)
	}
	return out
}

func decode(t *testing.T, path string) []Span {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read trace file: %v", err)
	}
	var spans []Span
	for _, line := range strings.Split(strings.TrimSpace(string(blob)), "\n") {
		if line == "" {
			continue
		}
		var payload struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []struct {
						Name         string `json:"name"`
						TraceID      string `json:"traceId"`
						ParentSpanID string `json:"parentSpanId"`
						Start        string `json:"startTimeUnixNano"`
						End          string `json:"endTimeUnixNano"`
						Attributes   []struct {
							Key   string         `json:"key"`
							Value map[string]any `json:"value"`
						} `json:"attributes"`
						Status struct {
							Code int `json:"code"`
						} `json:"status"`
					} `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("trace line is not valid JSON: %v", err)
		}
		for _, resource := range payload.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, raw := range scope.Spans {
					span := Span{
						Name:   raw.Name,
						Trace:  raw.TraceID,
						Parent: raw.ParentSpanID,
						Start:  nanos(raw.Start),
						End:    nanos(raw.End),
						Attrs:  map[string]any{},
						Failed: raw.Status.Code == 2,
					}
					for _, attr := range raw.Attributes {
						span.Attrs[attr.Key] = value(attr.Value)
					}
					spans = append(spans, span)
				}
			}
		}
	}
	return spans
}

// nanos reads back a timestamp. OTLP carries them as decimal strings, and one
// that will not parse becomes the zero time, which fails a bracketing
// assertion rather than passing it by accident.
func nanos(text string) time.Time {
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// value unwraps one OTLP attribute. Integers arrive as strings on the wire and
// come back as int64, so an assertion compares against a number rather than
// against the encoding.
func value(raw map[string]any) any {
	if text, ok := raw["intValue"].(string); ok {
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return text
		}
		return n
	}
	for _, key := range []string{"stringValue", "boolValue", "doubleValue"} {
		if got, ok := raw[key]; ok {
			return got
		}
	}
	return nil
}
