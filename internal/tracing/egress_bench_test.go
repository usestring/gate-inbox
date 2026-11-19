package tracing

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// spanBatch is a batch as the exporter sees one from a busy board: mostly
// short names and counters, with the strings a pass really carries -- a
// working directory, a tool, an error that wraps a path -- on some of them.
func spanBatch(n int) []Span {
	dir := "/" + strings.Join([]string{"home", "someone", "repos", "project"}, "/")
	start := time.Unix(1_700_000_000, 0)
	names := []string{"tmux.capture-pane", "poll.pass", "git.status", "session.turn", "mcp.tool"}
	spans := make([]Span, n)
	for i := range spans {
		attrs := []Attr{{Key: "sessions", Value: 40}, {Key: "live", Value: true}, {Key: "args", Value: 4}}
		var err error
		switch i % 4 {
		case 0:
			attrs = append(attrs, Attr{Key: "tool", Value: "Bash"}, Attr{Key: "state", Value: "working"})
		case 1:
			attrs = append(attrs, Attr{Key: "detail", Value: "exit status 1 after 3 retries"})
		case 2:
			err = errors.New("open " + dir + "/notes.md: permission denied")
		}
		spans[i] = Span{Name: names[i%len(names)], Start: start, End: start.Add(time.Millisecond), Attrs: attrs, Err: err}
	}
	return spans
}

// The cost of turning one batch into the body that is sent: on this branch it
// includes checking every string for a path.
func BenchmarkEncodeSpanBatch(b *testing.B) {
	spans := spanBatch(64)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := encode(spans); err != nil {
			b.Fatal(err)
		}
	}
}

// HidePaths alone, over the strings of that batch, as the exporter runs it.
func BenchmarkHidePathsSpanBatch(b *testing.B) {
	var values []string
	for _, s := range spanBatch(64) {
		values = append(values, s.Name)
		for _, a := range s.Attrs {
			if v, ok := a.Value.(string); ok {
				values = append(values, v)
			}
		}
		if s.Err != nil {
			values = append(values, s.Err.Error())
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, v := range values {
			HidePaths(v)
		}
	}
}
