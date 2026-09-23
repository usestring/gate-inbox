package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// tracedSpan is one span as a reader of the trace file gets it, which is the
// only shape that proves anything: a span that is right in memory and wrong
// on the wire is wrong.
type tracedSpan struct {
	Name         string `json:"name"`
	TraceID      string `json:"traceId"`
	SpanID       string `json:"spanId"`
	ParentSpanID string `json:"parentSpanId"`
	Start        string `json:"startTimeUnixNano"`
	End          string `json:"endTimeUnixNano"`
	Attributes   []struct {
		Key   string         `json:"key"`
		Value map[string]any `json:"value"`
	} `json:"attributes"`
}

func (s tracedSpan) value(t *testing.T, key string) map[string]any {
	t.Helper()
	for _, attr := range s.Attributes {
		if attr.Key == key {
			return attr.Value
		}
	}
	t.Fatalf("span %q carries no %q attribute: %v", s.Name, key, s.Attributes)
	return nil
}

func (s tracedSpan) str(t *testing.T, key string) string {
	t.Helper()
	got, ok := s.value(t, key)["stringValue"].(string)
	if !ok {
		t.Fatalf("span %q attribute %q is not a string: %v", s.Name, key, s.value(t, key))
	}
	return got
}

func (s tracedSpan) boolean(t *testing.T, key string) bool {
	t.Helper()
	got, ok := s.value(t, key)["boolValue"].(bool)
	if !ok {
		t.Fatalf("span %q attribute %q is not a bool: %v", s.Name, key, s.value(t, key))
	}
	return got
}

func (s tracedSpan) number(t *testing.T, key string) int {
	t.Helper()
	raw, ok := s.value(t, key)["intValue"].(string)
	if !ok {
		t.Fatalf("span %q attribute %q is not an int: %v", s.Name, key, s.value(t, key))
	}
	got, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("span %q attribute %q: %v", s.Name, key, err)
	}
	return got
}

func (s tracedSpan) began(t *testing.T) int64 { return s.nanos(t, "start", s.Start) }

func (s tracedSpan) ended(t *testing.T) int64 { return s.nanos(t, "end", s.End) }

func (s tracedSpan) nanos(t *testing.T, which, raw string) int64 {
	t.Helper()
	got, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("span %q %s timestamp: %v", s.Name, which, err)
	}
	return got
}

// recordSpans runs work with the tracer on and hands back what it wrote.
func recordSpans(t *testing.T, work func()) []tracedSpan {
	t.Helper()
	// Keep every span. A running manager samples all but a fiftieth of its
	// ordinary traffic away, which is what lets tracing stay on by default,
	// and a test that emits one span and asks whether it arrived would
	// otherwise fail forty-nine times in fifty.
	t.Setenv(tracing.SampleEnv, "1")
	path := filepath.Join(t.TempDir(), "spans.jsonl")
	tracer, err := tracing.Start("file:" + path)
	if err != nil {
		t.Fatalf("start tracer: %v", err)
	}
	if !tracing.Enabled() {
		t.Fatal("the tracer started but Enabled says otherwise, so nothing below is measuring anything")
	}
	work()
	if err := tracer.Close(); err != nil {
		t.Fatalf("close tracer: %v", err)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spans: %v", err)
	}
	var spans []tracedSpan
	for _, line := range strings.Split(strings.TrimSpace(string(blob)), "\n") {
		if line == "" {
			continue
		}
		var payload struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []tracedSpan `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("payload is not valid JSON: %v", err)
		}
		for _, resource := range payload.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				spans = append(spans, scope.Spans...)
			}
		}
	}
	return spans
}

func spansNamed(spans []tracedSpan, name string) []tracedSpan {
	var out []tracedSpan
	for _, span := range spans {
		if span.Name == name {
			out = append(out, span)
		}
	}
	return out
}

func oneSpan(t *testing.T, spans []tracedSpan, name string) tracedSpan {
	t.Helper()
	found := spansNamed(spans, name)
	if len(found) != 1 {
		var names []string
		for _, span := range spans {
			names = append(names, span.Name)
		}
		t.Fatalf("want exactly one %q span, got %d of them in %v", name, len(found), names)
	}
	return found[0]
}

// The felt number is a key press through its handler and out the far side of
// the frame it caused. Either half alone reports a board that is faster than
// the one the operator is looking at.
func TestKeyToPaintCoversTheHandlerAndTheFrameItCaused(t *testing.T) {
	spans := recordSpans(t, func() {
		m := shotModel()
		// The fixture carries no poll loop; every key press tells one what
		// the cursor is on, so it needs somewhere to say it.
		m.poller = &poller{}
		m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		_ = m.frame()
	})

	root := oneSpan(t, spans, "ui.key_to_paint")
	if root.ParentSpanID != "" {
		t.Fatalf("ui.key_to_paint hangs off %q, so it is not the root of its own trace", root.ParentSpanID)
	}
	if got := root.str(t, "key"); got != "letter" {
		t.Fatalf("the span names the key %q, want %q", got, "letter")
	}

	update := oneSpan(t, spans, "ui.update")
	frame := oneSpan(t, spans, "ui.frame")
	for _, child := range []tracedSpan{update, frame} {
		if child.TraceID != root.TraceID {
			t.Fatalf("%q is in trace %q, not the press's %q: it will not draw as one waterfall", child.Name, child.TraceID, root.TraceID)
		}
		if child.ParentSpanID != root.SpanID {
			t.Fatalf("%q hangs off %q, want the press's %q", child.Name, child.ParentSpanID, root.SpanID)
		}
	}

	// The press's span has to start where the handler started and end where
	// the frame ended, or it is measuring one of its halves twice.
	if root.Start != update.Start {
		t.Fatalf("the press starts at %s but its handler at %s", root.Start, update.Start)
	}
	if root.End != frame.End {
		t.Fatalf("the press ends at %s but its frame at %s", root.End, frame.End)
	}
	if root.ended(t) <= root.began(t) {
		t.Fatal("the press took no measurable time, so the clock is not being read around anything")
	}
	if frame.began(t) < update.ended(t) {
		t.Fatal("the frame starts before the handler finished: the two halves overlap")
	}
}

// A frame span that cannot say whether it repainted answers the wrong
// question: the reuse fast path is 72ns and a repaint is milliseconds, and
// averaging them together hides every slow frame there is.
func TestFrameSpanSaysWhetherItRepaintedOrReusedTheLastFrame(t *testing.T) {
	spans := recordSpans(t, func() {
		m := shotModel()
		_ = m.frame()
		m.frameUnchanged()
		_ = m.frame()
	})

	frames := spansNamed(spans, "ui.frame")
	if len(frames) != 2 {
		t.Fatalf("want a span per frame, got %d", len(frames))
	}
	if frames[0].boolean(t, "reused") {
		t.Fatal("the first frame reports itself reused, but there was nothing yet to reuse")
	}
	if !frames[1].boolean(t, "reused") {
		t.Fatal("the second frame repainted rather than reusing, or the span cannot tell the two apart")
	}
	for _, frame := range frames {
		if got := frame.number(t, "sessions"); got != 6 {
			t.Fatalf("the frame reports %d sessions, want 6: cost has to be readable against fleet size", got)
		}
		if w, h := frame.number(t, "width"), frame.number(t, "height"); w != 120 || h != 34 {
			t.Fatalf("the frame reports %dx%d, want 120x34", w, h)
		}
		if got := frame.str(t, "mode"); got != "list" {
			t.Fatalf("the frame reports mode %q, want %q", got, "list")
		}
	}
	if reused, repainted := frames[1], frames[0]; reused.ended(t)-reused.began(t) >= repainted.ended(t)-repainted.began(t) {
		t.Fatal("the reused frame is not cheaper than the repaint, so the spans are not timing what they name")
	}
}

// A handler that has gone slow has to be attributable without knowing which
// message was in flight, which is the whole reason the span is keyed by type.
func TestUpdateSpansAreKeyedByTheirMessageType(t *testing.T) {
	spans := recordSpans(t, func() {
		m := shotModel()
		m.mode = modeHelp
		m.Update(previewTickMsg{})
	})

	update := oneSpan(t, spans, "ui.update")
	if got := update.str(t, "msg"); got != "ui.previewTickMsg" {
		t.Fatalf("the span names the message %q, want %q", got, "ui.previewTickMsg")
	}
	if len(spansNamed(spans, "ui.key_to_paint")) != 0 {
		t.Fatal("a tick was reported as a key press, which puts time nobody waited on into the felt number")
	}
}

// The board is watched by its operator, and what that operator types into it
// is theirs. A printable key may be reported as what kind of key it was and
// never as the character, which no amount of "the trace file is local"
// changes: these spans ship to Axiom.
func TestAPrintableKeyIsNeverReportedAsTheCharacterTyped(t *testing.T) {
	categories := map[string]bool{"letter": true, "digit": true, "punct": true, "space": true}
	for code := rune(' '); code <= '~'; code++ {
		name := traceKeyName(tea.KeyPressMsg{Code: code, Text: string(code)})
		if !categories[name] {
			t.Fatalf("pressing %q is recorded as %q, which is not one of the categories: the keystroke itself is in the trace", string(code), name)
		}
	}
	// A press carrying several runes arrives with its text, which is the
	// pasted or composed content this must never keep.
	if name := traceKeyName(tea.KeyPressMsg{Code: tea.KeyExtended, Text: "acme-prod-credentials"}); strings.Contains(name, "acme") {
		t.Fatalf("a multi-rune press is recorded as %q", name)
	}
	// A control code carries no printable character, but writing it out
	// verbatim would still put a raw byte of the operator's input in the
	// trace; every one of them has to come back as a word.
	for code := rune(0); code < ' '; code++ {
		if name := traceKeyName(tea.KeyPressMsg{Code: code}); len([]rune(name)) <= 1 {
			t.Fatalf("control code %d is recorded as %q, a keystroke rather than a name", code, name)
		}
	}
}

// Which key is slow is a real question, and the categories alone cannot
// answer it for the keys that are not characters at all.
func TestNamedKeysAndModifiersSurviveTheCategories(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "enter"},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, "tab"},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEsc}, "esc"},
		{"space", tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, "space"},
		{"arrow", tea.KeyPressMsg{Code: tea.KeyDown}, "down"},
		{"ctrl+letter", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "ctrl+letter"},
		{"alt+arrow", tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}, "alt+left"},
		{"shift+letter", tea.KeyPressMsg{Code: 'a', Mod: tea.ModShift, Text: "A"}, "shift+letter"},
		// Fixed order, so one keystroke is one thing in a query.
		{"two mods", tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt | tea.ModCtrl}, "ctrl+alt+letter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := traceKeyName(tc.key); got != tc.want {
				t.Fatalf("traceKeyName = %q, want %q", got, tc.want)
			}
		})
	}
}

// Whatever a frame costs to watch, it must not cost anything to leave
// unwatched. These two run the identical path with the tracer off and on;
// the first is the number that has to match an uninstrumented build.
func BenchmarkFrameReuse(b *testing.B) {
	m := fleetModel(b, fleetSize, 246, 63)
	_ = m.frame()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.frameUnchanged()
		_ = m.frame()
	}
}

func BenchmarkFrameReuseTraced(b *testing.B) {
	defer benchTracer(b)()
	m := fleetModel(b, fleetSize, 246, 63)
	_ = m.frame()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.frameUnchanged()
		_ = m.frame()
	}
}

func BenchmarkListFrameTraced(b *testing.B) {
	defer benchTracer(b)()
	m := shotModel()
	m.width, m.height = 200, 50
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.frame()
	}
}

func BenchmarkUpdateTick(b *testing.B) {
	m := fleetModel(b, fleetSize, 246, 63)
	m.mode = modeHelp
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.Update(previewTickMsg{})
	}
}

func BenchmarkUpdateTickTraced(b *testing.B) {
	defer benchTracer(b)()
	m := fleetModel(b, fleetSize, 246, 63)
	m.mode = modeHelp
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(previewTickMsg{})
	}
}

func BenchmarkKeyToPaintTraced(b *testing.B) {
	defer benchTracer(b)()
	m := fleetModel(b, fleetSize, 246, 63)
	m.poller = &poller{}
	_ = m.frame()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		m.frameUnchanged()
		_ = m.frame()
	}
}

// benchTracer records to /dev/null. A benchmark emits spans far faster than
// an operator ever will, and what is being measured is what the event loop
// pays -- building the span and handing it off -- not what a disk does with
// millions of them afterwards.
func benchTracer(b *testing.B) func() {
	b.Helper()
	// What this measures is what the event loop pays for a span, so the
	// sampler must not decide most of them cost nothing.
	b.Setenv(tracing.SampleEnv, "1")
	tracer, err := tracing.Start("file:/dev/null")
	if err != nil {
		b.Fatalf("start tracer: %v", err)
	}
	return func() {
		// The exporter drops what it cannot keep up with, so a run that shed
		// most of its spans measured the handoff and not the export. Report
		// it rather than let the number read as the whole cost.
		b.ReportMetric(float64(tracing.Dropped())/float64(b.N), "shed/op")
		tracer.Close()
	}
}
