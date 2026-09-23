package search

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tracetest"
)

// A refresh is three phases that fail for different reasons, and a single
// duration for the pass cannot say which one it was in. The children are what
// tell a filesystem that has gone slow from a session that has been busy.
func TestARefreshReportsItsPhases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeLines(t, path, userLine("a query about pricing"), assistantLine("answered", "ls"))
	x := openTest(t, Options{})
	targets := []Target{{Key: "sess1", Tool: ToolClaude, Path: path}}

	spans := tracetest.Capture(t)
	progress, err := x.Refresh(targets, 1<<20)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	recorded := spans()

	root := tracetest.One(t, recorded, "search.refresh")
	if root.Parent != "" {
		t.Errorf("the refresh has parent %q; it is the root of its trace", root.Parent)
	}
	if root.Attr("targets") != int64(1) {
		t.Errorf("targets = %v, want 1", root.Attr("targets"))
	}
	if root.Attr("stale") != int64(1) {
		t.Errorf("stale = %v, want the one file that had grown", root.Attr("stale"))
	}
	if root.Attr("bytes") != int64(progress.Bytes) {
		t.Errorf("bytes = %v, want the %d the pass reported reading", root.Attr("bytes"), progress.Bytes)
	}
	if root.Attr("done") != true {
		t.Errorf("done = %v, want true: the pass finished inside its budget", root.Attr("done"))
	}
	for _, name := range []string{"search.scan", "search.read", "search.opencode"} {
		child := tracetest.One(t, recorded, name)
		if child.Trace != root.Trace || child.Parent == "" {
			t.Errorf("%s is not under the refresh: trace %q parent %q", name, child.Trace, child.Parent)
		}
	}
	if read := tracetest.One(t, recorded, "search.read"); read.Attr("files") != int64(1) {
		t.Errorf("search.read files = %v, want 1", read.Attr("files"))
	}
}

// A pass that reads nothing still stats every source, which is the steady
// state on a quiet board: if that pass were not recorded, the cheap case --
// the one that runs all day -- would be the one with no evidence behind it.
func TestAPassThatReadsNothingIsStillRecorded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeLines(t, path, userLine("hello"))
	x := openTest(t, Options{})
	targets := []Target{{Key: "sess1", Tool: ToolClaude, Path: path}}
	if _, err := x.Refresh(targets, 1<<20); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	spans := tracetest.Capture(t)
	if _, err := x.Refresh(targets, 1<<20); err != nil {
		t.Fatalf("second Refresh: %v", err)
	}
	recorded := spans()

	root := tracetest.One(t, recorded, "search.refresh")
	if root.Attr("stale") != int64(0) {
		t.Errorf("stale = %v, want 0: nothing had changed", root.Attr("stale"))
	}
	if root.Attr("changed") != false {
		t.Errorf("changed = %v, want false", root.Attr("changed"))
	}
}

// The operator is typing the query, so the span may say how long it was and
// never what it said. This is the one place in the board where a keystroke
// could reach a span, and it must not.
func TestAQueryIsMeasuredWithoutBeingRecorded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeLines(t, path, userLine("the deploy failed on staging"))
	x := openTest(t, Options{})
	targets := []Target{{Key: "sess1", Tool: ToolClaude, Path: path}}
	refreshAll(t, x, targets)

	query := "staging"
	spans := tracetest.Capture(t)
	found, ok, err := x.Search(query, 10)
	if err != nil || !ok || len(found) != 1 {
		t.Fatalf("Search = %v, %v, %v; want the one session", found, ok, err)
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "search.query")
	if span.Attr("query.runes") != int64(len(query)) {
		t.Errorf("query.runes = %v, want %d", span.Attr("query.runes"), len(query))
	}
	if span.Attr("hits") != int64(1) {
		t.Errorf("hits = %v, want 1", span.Attr("hits"))
	}
	if span.Attr("scanned") != int64(1) {
		t.Errorf("scanned = %v, want the one session the query read", span.Attr("scanned"))
	}
	if span.Attr("answered") != true {
		t.Errorf("answered = %v, want true", span.Attr("answered"))
	}
	for key, value := range span.Attrs {
		if text, ok := value.(string); ok && strings.Contains(strings.ToLower(text), query) {
			t.Fatalf("attribute %q carries the query itself: %q", key, text)
		}
	}
}

// A query too short for the index is not an error and not a miss: it is a
// query the index declined to answer, and the span has to keep those apart or
// the hit rate it reports is a fiction.
func TestATooShortQueryIsRecordedAsUnanswered(t *testing.T) {
	x := openTest(t, Options{})

	spans := tracetest.Capture(t)
	if _, ok, _ := x.Search("ab", 10); ok {
		t.Fatal("a two-rune query is below MinQueryRunes and must not be answered")
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "search.query")
	if span.Attr("answered") != false {
		t.Errorf("answered = %v, want false", span.Attr("answered"))
	}
	if span.Failed {
		t.Error("declining a short query is not a failure")
	}
}
