package promptcache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// assistantLine is a transcript record in the shape Claude Code writes.
func assistantLine(stamp time.Time, cacheRead, oneHour, fiveMinute int) string {
	return fmt.Sprintf(
		`{"type":"assistant","timestamp":%q,"message":{"usage":{"cache_read_input_tokens":%d,`+
			`"cache_creation_input_tokens":%d,`+
			`"cache_creation":{"ephemeral_1h_input_tokens":%d,"ephemeral_5m_input_tokens":%d}}}}`,
		stamp.UTC().Format(time.RFC3339Nano), cacheRead, oneHour+fiveMinute, oneHour, fiveMinute)
}

func writeTranscript(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// stampedAt fixes a fixture's modification time. A file that was just written
// takes its mtime from the wall clock, so a test ordering one fixture against
// another must stamp both: stamping only the one it wants newest makes the
// ordering hold until the clock passes that stamp and never again.
func stampedAt(t *testing.T, path string, at time.Time) string {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheTranscriptDecidesTheTTLRatherThanAGuess(t *testing.T) {
	stamp := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()

	long := writeTranscript(t, dir, "long.jsonl", assistantLine(stamp, 301_224, 408, 0))
	if state := ReadTranscript(long); state.TTL != TTLLong {
		t.Fatalf("ephemeral_1h_input_tokens > 0 means the hour TTL, got %v", state.TTL)
	}
	short := writeTranscript(t, dir, "short.jsonl", assistantLine(stamp, 88_000, 0, 25_519))
	if state := ReadTranscript(short); state.TTL != TTLShort {
		t.Fatalf("no ephemeral_1h_input_tokens means the five-minute TTL, got %v", state.TTL)
	}
}

func TestTheNewestAssistantRecordIsTheOneThatCounts(t *testing.T) {
	old := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	recent := old.Add(90 * time.Minute)
	path := writeTranscript(t, t.TempDir(), "t.jsonl",
		assistantLine(old, 10_000, 0, 1),
		`{"type":"user","timestamp":"2026-08-25T11:00:00Z","message":{"content":"a tool result"}}`,
		assistantLine(recent, 301_224, 408, 0),
		`{"type":"user","timestamp":"2026-08-25T11:31:00Z","message":{"content":"another"}}`,
	)
	state := ReadTranscript(path)
	if !state.Known || !state.LastTurnAt.Equal(recent) {
		t.Fatalf("got %+v, want the last assistant turn at %v", state, recent)
	}
	if state.ContextTokens != 301_224+408 {
		t.Fatalf("context = %d, want the cache a cold send would rebuild", state.ContextTokens)
	}
}

// A transcript grows past thirty megabytes, so only its tail is read. A very
// large tool result between the newest assistant record and the end of the
// file must not push that record out of view.
func TestALargeTailStillFindsTheNewestAssistantRecord(t *testing.T) {
	stamp := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	filler := `{"type":"user","timestamp":"2026-08-25T10:00:01Z","message":{"content":"` +
		strings.Repeat("x", 900<<10) + `"}}`
	path := writeTranscript(t, t.TempDir(), "big.jsonl", assistantLine(stamp, 42_000, 0, 1), filler)
	state := ReadTranscript(path)
	if !state.Known || state.ContextTokens != 42_001 {
		t.Fatalf("got %+v, want the record behind the large tail", state)
	}
}

func TestATranscriptWithNothingToReadIsNotKnown(t *testing.T) {
	path := writeTranscript(t, t.TempDir(), "empty.jsonl",
		`{"type":"user","timestamp":"2026-08-25T10:00:00Z"}`,
		`not json at all`)
	if state := ReadTranscript(path); state.Known {
		t.Fatalf("got %+v, want an unknown price", state)
	}
	if state := ReadTranscript(filepath.Join(t.TempDir(), "missing.jsonl")); state.Known {
		t.Fatalf("a missing file must be unknown, got %+v", state)
	}
}

func TestUsageIncludesUncachedTokensWithoutInventingAWarmCache(t *testing.T) {
	path := writeTranscript(t, t.TempDir(), "usage.jsonl",
		assistantLine(time.Now().Add(-time.Hour), 4000, 0, 100),
		`{"type":"assistant","timestamp":"2026-09-17T21:00:00Z","message":{"model":"claude-test","content":"private response","usage":{"input_tokens":1200,"output_tokens":75}}}`,
	)
	state := ReadTranscript(path)
	if state.Usage == nil || state.Usage.InputTokens != 1200 || state.Usage.OutputTokens != 75 || state.Model != "claude-test" {
		t.Fatalf("missing latest usage: %+v", state)
	}
	if state.Known || state.ContextTokens != 0 || state.Warm(time.Now(), 0) {
		t.Fatalf("uncached usage reused an old cache: %+v", state)
	}
}

func TestUsageRejectsNegativeTokenCounts(t *testing.T) {
	path := writeTranscript(t, t.TempDir(), "invalid.jsonl",
		assistantLine(time.Now().Add(-time.Hour), 4000, 0, 100),
		`{"type":"assistant","timestamp":"2026-09-17T21:00:00Z","message":{"usage":{"input_tokens":-1}}}`,
	)
	if state := ReadTranscript(path); state.Usage != nil || state.Known {
		t.Fatalf("invalid latest usage must not expose old usage: %+v", state)
	}
}

func TestSyntheticErrorsDoNotReplaceRealUsage(t *testing.T) {
	stamp := time.Now().Add(-time.Minute)
	for _, errorRecord := range []string{
		`{"type":"assistant","timestamp":"2026-09-17T20:32:13.925Z","isApiErrorMessage":true,"message":{"usage":{"input_tokens":0,"output_tokens":0}}}`,
		`{"type":"assistant","timestamp":"2026-09-17T20:32:13.925Z","message":{"model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0}}}`,
	} {
		path := writeTranscript(t, t.TempDir(), "error.jsonl", assistantLine(stamp, 99236, 2142, 0), errorRecord)
		state := ReadTranscript(path)
		if !state.Known || !state.LastTurnAt.Equal(stamp) || state.ContextTokens != 101378 {
			t.Fatalf("synthetic error hid real usage: %+v", state)
		}
	}
}

func TestWarmIsAForecastRatherThanAReading(t *testing.T) {
	base := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	state := State{Known: true, TTL: TTLShort, LastTurnAt: base.Add(-4 * time.Minute)}
	if !state.Warm(base, 30*time.Second) {
		t.Fatal("a minute of TTL left is warm against a thirty-second margin")
	}
	// Forty seconds of TTL left, but the margin says a send may not land for
	// a minute, so it counts as cold.
	if state.Warm(base.Add(20*time.Second), time.Minute) {
		t.Fatal("a session inside the margin must not read as warm")
	}
	if (State{}).Warm(base, time.Second) {
		t.Fatal("an unknown price is never warm")
	}
}

func TestMarginScalesWithTheTTLAndNeverVanishes(t *testing.T) {
	if got := Margin(TTLLong); got != 12*time.Minute {
		t.Fatalf("Margin(1h) = %v, want 12m", got)
	}
	if got := Margin(TTLShort); got != time.Minute {
		t.Fatalf("Margin(5m) = %v, want 1m", got)
	}
	if got := Margin(time.Second); got != 30*time.Second {
		t.Fatalf("Margin is floored at 30s, got %v", got)
	}
}

// The directory name is Claude Code's own mangling of the working directory,
// so a session is found by the path it runs in rather than by a guess.
func TestAProjectDirectoryIsTheMangledWorkingDirectory(t *testing.T) {
	reader := NewReader("/root")
	got := reader.ProjectDir("/home/user/repos/worktrees/feat-2026.08/go")
	want := "/root/-home-user-repos-worktrees-feat-2026-08-go"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAKnownConversationIDNamesItsTranscriptExactly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-repo")
	stamp := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	stampedAt(t, writeTranscript(t, dir, "aaaa.jsonl", assistantLine(stamp, 1_000, 0, 1)), stamp)
	newest := stampedAt(t,
		writeTranscript(t, dir, "bbbb.jsonl", assistantLine(stamp, 2_000, 0, 1)), stamp.Add(time.Hour))

	reader := NewReader(root)
	if got := reader.Transcript("/repo", "aaaa"); got != filepath.Join(dir, "aaaa.jsonl") {
		t.Fatalf("a known id must win over recency, got %q", got)
	}
	// An adopted pane has no id, so the newest transcript in its directory is
	// the best available answer.
	if got := reader.Transcript("/repo", ""); got != newest {
		t.Fatalf("got %q, want the newest transcript %q", got, newest)
	}
	if got := reader.Transcript("/repo", "no-such-id"); got != "" {
		t.Fatalf("an id with no file must resolve to nothing, got %q", got)
	}
}

// Subagent transcripts live in a subdirectory of the project directory and
// describe a different conversation, so they must never be picked up.
func TestSubagentTranscriptsAreNotTheSessionsOwn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-repo")
	stamp := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	own := stampedAt(t, writeTranscript(t, dir, "own.jsonl", assistantLine(stamp, 1_000, 0, 1)), stamp)
	stampedAt(t,
		writeTranscript(t, filepath.Join(dir, "subagents"), "agent-1.jsonl", assistantLine(stamp, 9, 0, 1)),
		stamp.Add(time.Hour))
	if got := NewReader(root).Transcript("/repo", ""); got != own {
		t.Fatalf("got %q, want the session's own transcript %q", got, own)
	}
}

// A conversation id arrives from a store row and is joined onto a path, so an
// id that is not a plain token must resolve to nothing rather than to a file
// somewhere else on disk.
func TestAnIDThatIsNotAPlainTokenResolvesToNothing(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, filepath.Join(root, "-repo"), "own.jsonl",
		assistantLine(time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC), 1_000, 0, 1))
	reader := NewReader(root)
	// An empty id is not in this set: it is the adopted-pane case, and means
	// "no id known, take the newest transcript here".
	for _, id := range []string{"../-repo/own", "a/b", " ", "own.jsonl"} {
		if got := reader.Transcript("/repo", id); got != "" {
			t.Errorf("Transcript(%q) = %q, want no path", id, got)
		}
	}
}
