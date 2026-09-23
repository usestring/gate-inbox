package tracing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
)

// OTLP's JSON encoding is written out by hand here rather than pulled in.
//
// The alternative was the OpenTelemetry SDK, which for this one exporter adds
// forty-three modules including grpc, protobuf and genproto to a program with
// seventeen direct dependencies, and moved an unrelated one's version when
// tried. OTLP/JSON is a stable wire format and Axiom maps it to the same
// fields either way, so the dependency buys nothing this cannot state in a
// hundred lines. What it does buy -- context propagation across goroutines --
// is not wanted: the spans here come from measurements a pass already took.
const (
	axiomEndpoint = "https://api.axiom.co/v1/traces"

	// DatasetEnv names the destination, and has to: Axiom rejects an ingest
	// to a dataset that does not exist, and creating one needs an org admin
	// rather than an ingest token.
	//
	// TokenEnv carries the token itself. Left unset, secret.go reads it
	// from a named Secret Manager secret on the first send, so turning
	// tracing on need not put a token in a profile or a history file.
	TokenEnv   = "AXIOM_TRACE_TOKEN"
	DatasetEnv = "GATE_INBOX_TRACE_DATASET"

	// givingUpAfter is how many consecutive failed sends end the attempt. A
	// board on a network that cannot reach Axiom must not spend the rest of
	// the day posting into it every two seconds, and the operator must not
	// have a log filling with the same line.
	givingUpAfter = 5

	// flushEvery bounds how long a span waits to be shipped. A trace that
	// arrives two seconds late is no less true, and batching is what keeps
	// this off the poll's clock.
	flushEvery = 2 * time.Second

	// queueDepth is how many pass-sized batches may be in flight. Two
	// hundred is minutes of passes: far more than a slow flush needs, and
	// small enough that a destination that has stopped answering costs
	// memory that is bounded rather than memory that is not.
	queueDepth = 200
)

// Tracer owns the export goroutine. One exists per process, or none.
type Tracer struct {
	queue   chan []Span
	done    chan struct{}
	wg      sync.WaitGroup
	dropped atomic.Int64
	// closeOnce guards done: Close is deferred at startup and also reachable
	// from a caller that wants the spans flushed early, and closing a closed
	// channel is a panic rather than a no-op.
	closeOnce sync.Once
	ship      func([]byte) error
	// closeSink releases whatever ship writes through, once the exporter has
	// stopped and the last batch is on disk.
	closeSink func() error
	// where is what a startup line tells the operator, so a run that is
	// recording says where to go and look.
	where string
}

// Start begins exporting, or returns (nil, nil) when spec is empty -- the
// case that must stay free, since it is every run that did not ask for this.
func Start(spec string) (*Tracer, error) {
	sink, target, err := Resolve(spec)
	if err != nil || sink == "" {
		return nil, err
	}
	tracer := &Tracer{queue: make(chan []Span, queueDepth), done: make(chan struct{})}
	switch sink {
	case "file":
		file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("trace file: %w", err)
		}
		var mu sync.Mutex
		tracer.where = target
		tracer.closeSink = file.Close
		tracer.ship = func(body []byte) error {
			mu.Lock()
			defer mu.Unlock()
			_, err := file.Write(append(body, '\n'))
			return err
		}
	case "axiom":
		dataset := strings.TrimSpace(os.Getenv(DatasetEnv))
		if dataset == "" {
			return nil, fmt.Errorf("%s=axiom needs %s: the dataset to send to", Env, DatasetEnv)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		tracer.where = axiomEndpoint + " dataset=" + dataset
		// Resolved on the exporter's goroutine, at the first send rather than
		// at startup: reading the secret shells out to gcloud and costs about
		// a second, which opening the board should not wait on.
		var (
			once     sync.Once
			token    string
			tokenErr error
		)
		tracer.ship = func(body []byte) error {
			once.Do(func() { token, tokenErr = resolveToken() })
			if tokenErr != nil {
				return tokenErr
			}
			return postOTLP(client, token, dataset, body)
		}
	}
	tracer.wg.Add(1)
	go tracer.run()
	live.Store(tracer)
	return tracer, nil
}

// Where names the destination, for the line that tells an operator a run is
// recording and where the spans went.
func (t *Tracer) Where() string {
	if t == nil {
		return ""
	}
	return t.where
}

// Close stops exporting and flushes what is already queued. It is safe on a
// nil Tracer so a caller can defer it without first testing whether tracing
// was ever switched on.
func (t *Tracer) Close() error {
	if t == nil {
		return nil
	}
	live.Store(nil)
	t.closeOnce.Do(func() { close(t.done) })
	t.wg.Wait()
	if t.closeSink != nil {
		return t.closeSink()
	}
	return nil
}

// stop takes this tracer off the recording path without tearing down the
// goroutine that is running inside it. Callers keep enqueueing into a channel
// nobody drains only until the next atomic load, which is the next span.
func (t *Tracer) stop() {
	live.CompareAndSwap(t, nil)
}

func (t *Tracer) enqueue(spans []Span) {
	select {
	case t.queue <- spans:
	default:
		t.dropped.Add(1)
	}
}

func (t *Tracer) run() {
	defer t.wg.Done()
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()
	var batch []Span
	failures := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		body, err := encode(batch)
		batch = nil
		if err != nil {
			return
		}
		// A destination that refuses the batch loses it. Retrying would
		// grow the queue behind a board that is already struggling, which
		// is the one thing this must not do to the program it measures.
		if err := t.ship(body); err != nil {
			failures++
			if failures == givingUpAfter {
				logging.Warn("tracing gave up; nothing further will be sent",
					"to", t.where, "err", err)
				t.stop()
			}
			return
		}
		failures = 0
	}
	for {
		select {
		case spans := <-t.queue:
			batch = append(batch, spans...)
			if len(batch) >= 512 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-t.done:
			// Drain what is already queued before going: the spans from the
			// run that just ended are the ones somebody is about to read.
			for {
				select {
				case spans := <-t.queue:
					batch = append(batch, spans...)
					continue
				default:
				}
				break
			}
			flush()
			return
		}
	}
}

func postOTLP(client *http.Client, token, dataset string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, axiomEndpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Axiom-Dataset", dataset)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("axiom traces: %s", resp.Status)
	}
	return nil
}

// encode renders spans as one OTLP/JSON ExportTraceServiceRequest.
func encode(spans []Span) ([]byte, error) {
	out := make([]any, 0, len(spans))
	for _, span := range spans {
		status := map[string]any{"code": 1}
		if span.Err != nil {
			status = map[string]any{"code": 2, "message": span.Err.Error()}
		}
		out = append(out, map[string]any{
			"traceId":           span.traceID,
			"spanId":            span.spanID,
			"parentSpanId":      span.parentID,
			"name":              span.Name,
			"kind":              1, // INTERNAL
			"startTimeUnixNano": strconv.FormatInt(span.Start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(span.End.UnixNano(), 10),
			"attributes":        encodeAttrs(span.Attrs),
			"status":            status,
		})
	}
	return json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{
		"resource": map[string]any{"attributes": encodeAttrs(resource())},
		"scopeSpans": []any{map[string]any{
			"scope": map[string]any{"name": ServiceName},
			"spans": out,
		}},
	}}})
}

func encodeAttrs(attrs []Attr) []any {
	out := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, map[string]any{"key": attr.Key, "value": encodeValue(attr.Value)})
	}
	return out
}

// encodeValue renders one attribute. OTLP/JSON carries 64-bit integers as
// strings, which is the detail a hand-written encoder gets wrong and then
// reports as a field Axiom cannot aggregate.
func encodeValue(value any) map[string]any {
	switch typed := value.(type) {
	case string:
		return map[string]any{"stringValue": typed}
	case bool:
		return map[string]any{"boolValue": typed}
	case int:
		return map[string]any{"intValue": strconv.Itoa(typed)}
	case int64:
		return map[string]any{"intValue": strconv.FormatInt(typed, 10)}
	case float64:
		return map[string]any{"doubleValue": typed}
	case time.Duration:
		// Milliseconds, because every question asked of these spans is asked
		// in milliseconds and a nanosecond count reads as noise.
		return map[string]any{"doubleValue": float64(typed) / float64(time.Millisecond)}
	default:
		return map[string]any{"stringValue": fmt.Sprintf("%v", typed)}
	}
}
