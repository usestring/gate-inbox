package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// recordedSpan is one span as whoever reads the trace file sees it, with the
// attributes already flattened: the assertions below are about what a query
// over these would find, not about OTLP's shape.
type recordedSpan struct {
	name  string
	attrs map[string]any
}

// traceToFile points the tracer at a file this test can read back.
func traceToFile(t *testing.T) (string, *tracing.Tracer) {
	t.Helper()
	// Keep every span. A running manager samples all but a fiftieth of its
	// ordinary traffic away, which is what lets tracing stay on by default,
	// and a test that runs one query and asks whether its span arrived would
	// otherwise fail forty-nine times in fifty.
	t.Setenv(tracing.SampleEnv, "1")
	path := filepath.Join(tmuxtest.ScratchDir(t), "spans.jsonl")
	tracer, err := tracing.Start("file:" + path)
	if err != nil {
		t.Fatalf("start tracing: %v", err)
	}
	t.Cleanup(func() { tracer.Close() })
	return path, tracer
}

func readSpans(t *testing.T, path string) []recordedSpan {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	var spans []recordedSpan
	for _, line := range strings.Split(strings.TrimSpace(string(blob)), "\n") {
		if line == "" {
			continue
		}
		var payload struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []struct {
						Name       string `json:"name"`
						Attributes []struct {
							Key   string         `json:"key"`
							Value map[string]any `json:"value"`
						} `json:"attributes"`
					} `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("trace payload is not valid JSON: %v", err)
		}
		for _, rs := range payload.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, raw := range ss.Spans {
					span := recordedSpan{name: raw.Name, attrs: map[string]any{}}
					for _, attr := range raw.Attributes {
						for kind, value := range attr.Value {
							// OTLP carries an integer as a string, so it is
							// turned back into one here rather than making
							// every assertion below spell out the encoding.
							if kind == "intValue" {
								n, err := strconv.Atoi(value.(string))
								if err != nil {
									t.Fatalf("%s attribute %q is not an integer: %v", raw.Name, attr.Key, err)
								}
								span.attrs[attr.Key] = n
								continue
							}
							span.attrs[attr.Key] = value
						}
					}
					spans = append(spans, span)
				}
			}
		}
	}
	return spans
}

// spanNamed returns the one span with this name and these attribute values,
// so a test can say which of two ListSessions calls it means.
func spanNamed(t *testing.T, spans []recordedSpan, name string, want map[string]any) recordedSpan {
	t.Helper()
	var found []recordedSpan
	for _, span := range spans {
		if span.name != name {
			continue
		}
		matched := true
		for key, value := range want {
			if span.attrs[key] != value {
				matched = false
				break
			}
		}
		if matched {
			found = append(found, span)
		}
	}
	if len(found) != 1 {
		var names []string
		for _, span := range spans {
			names = append(names, span.name)
		}
		t.Fatalf("%d spans named %q with %v; recorded: %s", len(found), name, want, strings.Join(names, ", "))
	}
	return found[0]
}

// The spans are what makes "what did the database cost this pass" answerable,
// so the names and the attributes a reader would group by are the contract.
func TestStoreOperationsArriveAsNamedSpans(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := st.CreateSession(sample(id, "g1")); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	if err := st.SetArchived("c", true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Started only now, so the rows above are seeded without spans of their
	// own and every span below belongs to a call this test made on purpose.
	path, tracer := traceToFile(t)
	if _, err := st.ListSessions(false); err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := st.ListSessions(true); err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if _, err := st.HeadMessages(); err != nil {
		t.Fatalf("heads: %v", err)
	}
	if _, err := st.LimitRecoveries(); err != nil {
		t.Fatalf("limit recoveries: %v", err)
	}
	if err := st.ApplyDerivedStates(time.Now(), []DerivedState{
		{ID: "a", Status: status.Working},
		{ID: "b", Status: status.Idle},
	}); err != nil {
		t.Fatalf("derived states: %v", err)
	}
	tracer.Close()

	spans := readSpans(t, path)
	// The active list and the archived one are the same span name and cost
	// several times apart, which is why the flag is on the span at all.
	active := spanNamed(t, spans, "store.ListSessions", map[string]any{"archived": false})
	if active.attrs["rows"] != 2 {
		t.Fatalf("the active list reported %v rows, want the 2 unarchived sessions", active.attrs["rows"])
	}
	if active.attrs["write"] != false {
		t.Fatalf("a list reported write=%v", active.attrs["write"])
	}
	archived := spanNamed(t, spans, "store.ListSessions", map[string]any{"archived": true})
	if archived.attrs["rows"] != 3 {
		t.Fatalf("the archived list reported %v rows, want all 3 sessions", archived.attrs["rows"])
	}
	spanNamed(t, spans, "store.HeadMessages", map[string]any{"write": false, "rows": 0})
	spanNamed(t, spans, "store.LimitRecoveries", map[string]any{"write": false, "rows": 0})
	// A write's row count is the number of rows the transaction carried,
	// which is the only thing separating a cheap pass from an expensive one.
	spanNamed(t, spans, "store.ApplyDerivedStates", map[string]any{"write": true, "rows": 2})
}

// The default build must not pay for instrumentation nobody asked for.
// Building a span allocates -- the attributes escape into the exporter's
// queue -- so the guard is not a nicety, and the proof is that the exported
// call and the bare query underneath it allocate the same while nothing is
// recording.
func TestAnUntracedCallAllocatesNothingExtra(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := st.CreateSession(sample(id, "g1")); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	if tracing.Enabled() {
		t.Fatal("a tracer is running before the measurement, so nothing here measures the default build")
	}
	bare := testing.AllocsPerRun(20, func() { _, _ = st.listSessions(false) })
	guarded := testing.AllocsPerRun(20, func() { _, _ = st.ListSessions(false) })
	if guarded > bare {
		t.Fatalf("the guarded call allocated %.0f against the query's own %.0f: the span is being built with nobody listening", guarded, bare)
	}

	// And the measurement has to be able to see a span at all, or the
	// comparison above would hold just as well for instrumentation that
	// never ran.
	_, tracer := traceToFile(t)
	recording := testing.AllocsPerRun(20, func() { _, _ = st.ListSessions(false) })
	tracer.Close()
	if recording <= bare {
		t.Fatalf("recording allocated %.0f against %.0f untraced: this measurement cannot see a span, so it cannot tell a guarded call from an unguarded one", recording, bare)
	}
}
