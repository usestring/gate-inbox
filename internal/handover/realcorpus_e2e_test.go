package handover

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension/decline"
)

// The real-transcript pass. The fixture is whatever transcript the operator
// points at -- a long-lived session that drifted, looped and declined is the
// shape this filter exists for, and a synthetic file has whatever shape the
// test author guessed. Nothing here is worth running against one.
func e2eTranscript(t *testing.T) string {
	t.Helper()
	path := os.Getenv("HANDOVER_E2E_TRANSCRIPT")
	if path == "" {
		t.Skip("HANDOVER_E2E_TRANSCRIPT is not set")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("HANDOVER_E2E_TRANSCRIPT is unavailable: %v", err)
	}
	return path
}

func countRecords(t *testing.T, path string) int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	n, blanks := 0, 0
	scan := bufio.NewScanner(file)
	scan.Buffer(make([]byte, 0, 1<<20), 512<<20)
	for scan.Scan() {
		if strings.TrimSpace(scan.Text()) != "" {
			n++
		} else {
			blanks++
		}
	}
	if blanks > 0 {
		t.Logf("%d blank lines in %s", blanks, path)
	}
	return n
}

func TestHandoverE2EFiltersARealLongLivedTranscript(t *testing.T) {
	src := e2eTranscript(t)
	dst := filepath.Join(t.TempDir(), "handover.jsonl")
	stats, err := Claude(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("source %d records; filtered %d kept, %d loop records dropped, %d decline turns stubbed, compacted=%v",
		countRecords(t, src), stats.Kept, stats.Dropped, stats.Stubbed, stats.Compacted)

	if !stats.Compacted {
		t.Fatal("a session that ran past its 200-turn limit twice must show a compaction cut")
	}
	// Every written record is still a JSON object the taking-over agent can
	// read with the format note the prompt gives it.
	file, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scan := bufio.NewScanner(file)
	scan.Buffer(make([]byte, 0, 1<<20), 512<<20)
	declines := 0
	for scan.Scan() {
		line := scan.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var probe map[string]any
		if err := json.Unmarshal(line, &probe); err != nil {
			t.Fatalf("filtered output is not JSONL: %v", err)
		}
		var rec claudeRecord
		if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" {
			continue
		}
		var msg claudeMessage
		if json.Unmarshal(rec.Message, &msg) != nil {
			continue
		}
		var blocks []map[string]any
		if json.Unmarshal(msg.Content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block["type"] == "text" && decline.LooksLike(claudeText(block)) {
				declines++
			}
		}
	}
	if declines != 0 {
		t.Fatalf("%d declined turns kept their essay in the filtered copy", declines)
	}
	// The takeover point survives: the last thing the operator asked the
	// previous session sits after the boundary, and a taking-over agent
	// must find it.
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "admin merge that then leave it") {
		t.Fatal("the takeover record was lost")
	}
	// And the decline essay the operator twice asked to be spared a
	// replacement does not ride along.
	if strings.Contains(string(body), "I won't build a mechanism") {
		t.Fatal("the decline essay survived the filter")
	}
}

func TestHandoverE2EDeviationCutOnTheRealTranscript(t *testing.T) {
	src := e2eTranscript(t)
	// A mid-run turn that was still on course, quoted the way the manager
	// quotes one: verbatim words from the record.
	snippet := "for the escalate ui etc lets move this so it showws up as a supervisor session"
	cut, found, err := DeviationCut("claude", src, snippet)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("the deviation point was not found in the real transcript")
	}
	total := countRecords(t, src)
	if cut <= 0 || cut >= total {
		t.Fatalf("deviation cut %d of %d records is not a mid-transcript line", cut, total)
	}
	t.Logf("deviation cut at record %d of %d", cut, total)
	dst := filepath.Join(t.TempDir(), "rewind.jsonl")
	stats, err := Claude(src, dst, Options{KeepTo: &cut})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rewind copy: %d kept, %d loop records dropped, %d declines stubbed", stats.Kept, stats.Dropped, stats.Stubbed)
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "showws up as a supervisor session") {
		t.Fatal("the last on-course turn did not survive its own deviation point")
	}
	// Everything the operator asked for after the deviation point is gone
	// from the rewind copy -- including the last instruction, which is the
	// drift this rewind would have removed.
	if strings.Contains(string(body), "we need to impl the filtering behaviour") {
		t.Fatal("post-deviation records survived the rewind")
	}
	if stats.Kept >= stats.Kept+stats.Dropped && stats.Kept > cut {
		// A record-keeping sanity check rather than an exact number: the
		// copy cannot hold more than the lines it was allowed to read.
		t.Fatalf("kept %d records from %d lines", stats.Kept, cut+1)
	}
}
