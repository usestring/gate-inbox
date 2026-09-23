package tmux

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tracing"
)

// emptyServer answers the way a tmux server that is up and holds no session
// does: it resolves a current session before it runs a listing, even one that
// names no target, so it fails that resolution instead of printing the empty
// listing it has. Reproduced against tmux 3.4 with "start-server ; set-option
// -g exit-empty off" and no sessions.
const emptyServer = `echo "no current target" >&2
exit 1
`

// A manager sits on exactly this server between its last session exiting and
// tmux's own exit-empty shutdown, and it is the same answer as a server that
// is down: this server holds nothing of mine. Read as a failure it failed the
// whole liveness pass, so no session's status could be resolved at all --
// scanPanes treats an unreadable server as fatal on purpose, because calling
// one empty would report every session on it gone.
func TestAnEmptyServerDoesNotFailAScan(t *testing.T) {
	driver := fakeTmux(t, emptyServer)

	scan, err := driver.ScanPanes()
	if err != nil {
		t.Fatalf("ScanPanes() on a running but empty server = %v, want no error", err)
	}
	if len(scan.PIDs) != 0 {
		t.Errorf("an empty server named panes: %v", scan.PIDs)
	}
	if len(scan.Gone) != 0 {
		t.Errorf("a scan with no adopted sessions proves nothing gone: %v", scan.Gone)
	}
}

// The consequence worth its own test: an adopted pane on a foreign server
// that is up and empty is proven gone, and a row is removed on that word.
// Zero sessions is zero panes, so it is the same evidence the scan already
// accepts from a socket with no server on it at all -- see
// TestScanPanesCountsAServerThatIsNotRunningAsGone. What it must not do is
// what it did before, which was fail the pass and resolve nothing.
func TestAnEmptyForeignServerLeavesItsAdoptedPaneGone(t *testing.T) {
	driver := fakeTmux(t, `case " $* " in
  *" -L foreignsock "*) echo "no current target" >&2; exit 1 ;;
esac
exit 0
`)
	adoptedID := uniqueID("onanemptyserver")
	adopt(t, driver, adoptedID, "foreignsock", "%1")

	scan, err := driver.ScanPanes()
	if err != nil {
		t.Fatalf("ScanPanes() across an empty foreign server = %v, want no error", err)
	}
	if !scan.Gone[adoptedID] {
		t.Errorf("a pane on a server holding no sessions is gone, got Gone = %v", scan.Gone)
	}
	if _, listed := scan.PIDs[adoptedID]; listed {
		t.Errorf("an empty server named a pid for %s: %v", adoptedID, scan.PIDs)
	}
}

// The three messages are the whole contract with tmux's wording, and the
// third is the one that is easy to lose again: it does not mention a server
// at all.
func TestNoServerRecognizesEveryEmptyAnswer(t *testing.T) {
	for _, out := range []string{
		"no server running on /tmp/tmux-1000/default",
		"error connecting to /tmp/tmux-1000/default (No such file or directory)",
		"no current target",
	} {
		if !noServer(out) {
			t.Errorf("noServer(%q) = false, want true", out)
		}
	}
	// A failure that names a target is a real one: the server answered and
	// said the thing asked for is not there, which is not the same as having
	// nothing to answer with.
	for _, out := range []string{
		"can't find session: gi_1e261057",
		"no such window: @1",
		"",
	} {
		if noServer(out) {
			t.Errorf("noServer(%q) = true, want false", out)
		}
	}
}

// A server with nothing to answer for must not reach the error-sampled
// bucket. The tail sampler keeps every errored span unconditionally, so this
// classification is what that bucket is worth: left as an error it was 50 of
// one hour's 51 errored list-panes spans, crowding out the one real failure.
func TestAServerWithNothingToAnswerForIsNotAnErrorSpan(t *testing.T) {
	path, tracer := traceToFile(t)
	driver := fakeTmux(t, emptyServer)

	if _, err := driver.ScanPanes(); err != nil {
		t.Fatalf("ScanPanes: %v", err)
	}
	tracer.Close()

	span := onlySpan(t, path, "tmux.list-panes")
	if code := statusCode(span); code != 1 {
		t.Errorf("status code %v, want 1 (OK): an expected empty answer is not an error", code)
	}
	if got := attr(span, "outcome"); got != "no-sessions" {
		t.Errorf("outcome attribute = %q, want %q so the answer is still readable", got, "no-sessions")
	}
}

// The complement, and the reason the classification above is narrow: a
// deadline that fired says nothing about the server, so it stays the error it
// is. combinedWithin reports it over empty output, which is exactly what must
// not be mistaken for tmux having said "nothing here".
func TestATimedOutScanIsStillAnErrorSpan(t *testing.T) {
	path, tracer := traceToFile(t)
	driver := fakeTmux(t, hangingOn("scan-timeout-test"))

	if _, err := driver.ScanPanes(); !errors.Is(err, ErrTimeout) {
		t.Fatalf("ScanPanes() error = %v, want %v", err, ErrTimeout)
	}
	tracer.Close()

	span := onlySpan(t, path, "tmux.list-panes")
	if code := statusCode(span); code != 2 {
		t.Errorf("status code %v, want 2 (ERROR): a timeout is a failure", code)
	}
	if got := attr(span, "outcome"); got != "" {
		t.Errorf("outcome attribute = %q, want none: nothing was answered", got)
	}
}

// traceToFile points the package's tracer at a file for the duration of one
// test, with sampling off so that what the call recorded is what the file
// holds.
func traceToFile(t *testing.T) (string, *tracing.Tracer) {
	t.Helper()
	t.Setenv(tracing.SampleEnv, "1")
	path := filepath.Join(t.TempDir(), "spans.jsonl")
	tracer, err := tracing.Start("file:" + path)
	if err != nil {
		t.Fatalf("tracing.Start: %v", err)
	}
	t.Cleanup(func() { tracer.Close() })
	return path, tracer
}

// onlySpan is the one span of a given name in the file. More than one means
// the test drove more tmux calls than it meant to and is asserting about the
// wrong one.
func onlySpan(t *testing.T, path, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spans: %v", err)
	}
	var found []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
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
				for _, span := range ss.Spans {
					if span["name"] == name {
						found = append(found, span)
					}
				}
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("found %d %s spans, want 1", len(found), name)
	}
	return found[0]
}

// statusCode is the span's OTLP status: 1 for OK, 2 for ERROR. A span with
// no status at all reads as 0, which is neither, so a missing one fails an
// assertion rather than passing the one it happens to match.
func statusCode(span map[string]any) float64 {
	status, ok := span["status"].(map[string]any)
	if !ok {
		return 0
	}
	code, ok := status["code"].(float64)
	if !ok {
		return 0
	}
	return code
}

func attr(span map[string]any, key string) string {
	attrs, _ := span["attributes"].([]any)
	for _, entry := range attrs {
		pair, ok := entry.(map[string]any)
		if !ok || pair["key"] != key {
			continue
		}
		value, ok := pair["value"].(map[string]any)
		if !ok {
			continue
		}
		text, _ := value["stringValue"].(string)
		return text
	}
	return ""
}
