package tracing

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decode reads the spans a run wrote, the way a reader of the file would.
func decode(t *testing.T, path string) []map[string]any {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	var spans []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(blob)), "\n") {
		if line == "" {
			continue
		}
		var payload struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []map[string]any `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("payload is not valid JSON: %v", err)
		}
		for _, rs := range payload.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				spans = append(spans, ss.Spans...)
			}
		}
	}
	return spans
}

func tracing(t *testing.T) (string, *Tracer) {
	t.Helper()
	// These tests are about what reaches the file, so nothing may be sampled
	// away between the call and the write.
	t.Setenv(SampleEnv, "1")
	path := filepath.Join(t.TempDir(), "spans.jsonl")
	tracer, err := Start("file:" + path)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { tracer.Close() })
	return path, tracer
}

// A trace has to arrive as one tree, or the waterfall it exists to draw is a
// pile of unrelated spans.
func TestPhasesArriveUnderTheirPass(t *testing.T) {
	path, tracer := tracing(t)
	start := time.Now()
	trace := NewTrace("poll.pass", start, start.Add(90*time.Millisecond), Attr{Key: "sessions", Value: 80})
	trace.Child("scan", start, start.Add(10*time.Millisecond))
	trace.Child("capture", start.Add(10*time.Millisecond), start.Add(60*time.Millisecond),
		Attr{Key: "derive.status", Value: 30 * time.Millisecond})
	trace.Emit()
	tracer.Close()

	spans := decode(t, path)
	if len(spans) != 3 {
		t.Fatalf("wrote %d spans, want 3", len(spans))
	}
	var root map[string]any
	for _, span := range spans {
		if span["name"] == "poll.pass" {
			root = span
		}
	}
	if root == nil {
		t.Fatal("no poll.pass span")
	}
	if root["parentSpanId"] != "" {
		t.Fatalf("the pass has a parent %q; it is the root", root["parentSpanId"])
	}
	for _, span := range spans {
		if span["name"] == "poll.pass" {
			continue
		}
		if span["traceId"] != root["traceId"] {
			t.Fatalf("%v is in trace %v, not the pass's %v", span["name"], span["traceId"], root["traceId"])
		}
		if span["parentSpanId"] != root["spanId"] {
			t.Fatalf("%v hangs off %v, not the pass", span["name"], span["parentSpanId"])
		}
	}
}

// OTLP/JSON carries 64-bit integers as strings. Emitting them as numbers is
// accepted by a JSON parser and then silently arrives as a field the backend
// will not aggregate, which is the failure a reader discovers weeks later.
func TestIntegerAttributesAreEncodedAsStrings(t *testing.T) {
	path, tracer := tracing(t)
	now := time.Now()
	Record("tmux.capture-pane", now, now.Add(time.Millisecond), nil, Attr{Key: "args", Value: 7})
	tracer.Close()

	spans := decode(t, path)
	if len(spans) != 1 {
		t.Fatalf("wrote %d spans, want 1", len(spans))
	}
	attrs, _ := spans[0]["attributes"].([]any)
	// The sampler stamps its own attributes alongside the caller's, so this
	// looks the one under test up by name rather than by position.
	var value map[string]any
	for _, attr := range attrs {
		entry := attr.(map[string]any)
		if entry["key"] == "args" {
			value = entry["value"].(map[string]any)
		}
	}
	if value == nil {
		t.Fatalf("the args attribute did not survive: %v", attrs)
	}
	got, ok := value["intValue"]
	if !ok {
		t.Fatalf("an int attribute encoded as %v, want an intValue", value)
	}
	if _, isString := got.(string); !isString {
		t.Fatalf("intValue is %T (%v), want a string", got, got)
	}
}

// An error has to reach the span, or a failing tmux call reads as a fast one.
func TestAFailedCallIsRecordedAsAnError(t *testing.T) {
	path, tracer := tracing(t)
	now := time.Now()
	Record("tmux.has-session", now, now.Add(time.Millisecond), errors.New("no server"))
	tracer.Close()

	spans := decode(t, path)
	status := spans[0]["status"].(map[string]any)
	if status["code"] != float64(2) {
		t.Fatalf("status code %v, want 2 (ERROR)", status["code"])
	}
	if status["message"] != "no server" {
		t.Fatalf("status message %q lost the error", status["message"])
	}
}

// Telemetry that can stall the program it measures is worse than none, and
// stalls are this program's whole problem. A destination that stops draining
// must cost dropped spans, never a blocked poll.
func TestAFullQueueDropsInsteadOfBlocking(t *testing.T) {
	// The sampler would otherwise drop these long before the queue could,
	// and this is a test about the queue.
	t.Setenv(SampleEnv, "1")
	tracer := &Tracer{queue: make(chan []Span, 2), done: make(chan struct{})}
	live.Store(tracer)
	t.Cleanup(func() { live.Store(nil) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 50 {
			Record("tmux.capture-pane", time.Now(), time.Now(), nil)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recording blocked on a queue nothing was draining")
	}
	if Dropped() == 0 {
		t.Fatal("nothing was reported dropped, so the overflow went unaccounted for")
	}
}

// Off has to mean off, and has to be reachable: a shared machine, a test, or
// an operator who simply does not want it needs one word that stops it dead.
func TestOffMeansNothingIsRecorded(t *testing.T) {
	live.Store(nil)
	if Enabled() {
		t.Fatal("tracing reports itself on with no tracer")
	}
	// Neither call may touch anything; the test is that they are safe.
	Record("tmux.capture-pane", time.Now(), time.Now(), nil)
	NewTrace("poll.pass", time.Now(), time.Now()).Emit()

	tracer, err := Start("off")
	if err != nil || tracer != nil {
		t.Fatalf("Start(\"off\") = %v, %v; want no tracer and no error", tracer, err)
	}
}

// Unset means off. A board nobody configured sends nothing anywhere, even
// with a token lying in its environment.
func TestAnUnsetSettingTracesNothing(t *testing.T) {
	t.Setenv(TokenEnv, "xaat-literal")
	t.Setenv(DatasetEnv, "somewhere")
	sink, target, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve(\"\"): %v", err)
	}
	if sink != "" || target != "" {
		t.Fatalf("an unset setting resolved to %q/%q, want nothing", sink, target)
	}
	tracer, err := Start("")
	if err != nil || tracer != nil {
		t.Fatalf("Start(\"\") = %v, %v; want no tracer and no error", tracer, err)
	}
}

func TestResolveRejectsASpecItCannotHonour(t *testing.T) {
	for _, spec := range []string{"file:", "loki", "axiom:hmm"} {
		if _, _, err := Resolve(spec); err == nil {
			t.Fatalf("Resolve(%q) was accepted; an unreadable setting must be reported, not ignored", spec)
		}
	}
}
