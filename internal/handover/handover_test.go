package handover

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The synthetic fixtures first: every rule the filter applies needs a case
// that fails when the rule is wrong, not only when the file happens to be
// shaped like one.

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const declEssay = "I can't help with this goal. It asks for changes outside the scope this task was given, so I am stopping here rather than advancing the task."

func claudeAssistant(text string, extra ...map[string]any) string {
	blocks := append([]map[string]any{{"type": "text", "text": text}}, extra...)
	rec := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": blocks,
		},
	}
	raw, _ := json.Marshal(rec)
	return string(raw)
}

func claudeUser(content any) string {
	rec := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
	}
	raw, _ := json.Marshal(rec)
	return string(raw)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var out []string
	scan := bufio.NewScanner(file)
	scan.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scan.Scan() {
		if strings.TrimSpace(scan.Text()) != "" {
			out = append(out, scan.Text())
		}
	}
	return out
}

func TestClaudeFilterCollapsesLoopSpam(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.jsonl")
	call := func(n int) string {
		rec := map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "tool_use", "id": "toolu-loop" + string(rune('a'+n)), "name": "Bash", "input": map[string]any{"command": "ls -la"}},
				},
			},
		}
		raw, _ := json.Marshal(rec)
		return string(raw)
	}
	writeLines(t, src,
		claudeUser("list the directory"),
		call(0), call(1), call(2), call(3), call(4),
		claudeAssistant("here is the listing"))
	dst := filepath.Join(t.TempDir(), "dst.jsonl")
	stats, err := Claude(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Dropped != 4 {
		t.Fatalf("loop collapse: dropped %d, want 4", stats.Dropped)
	}
	lines := readLines(t, dst)
	if len(lines) != 3 {
		t.Fatalf("kept %d records, want 3 (the ask, the first call, the answer)", len(lines))
	}
}

func TestClaudeFilterStubsADeclinedTurnIncludingItsThinking(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.jsonl")
	writeLines(t, src,
		claudeUser("advance the goal"),
		claudeAssistant(declEssay, map[string]any{"type": "thinking", "text": "the reasoning that led to the decline, at length"}),
		claudeAssistant("meanwhile, work that was done"),
	)
	dst := filepath.Join(t.TempDir(), "dst.jsonl")
	stats, err := Claude(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Stubbed != 1 {
		t.Fatalf("stubbed %d turns, want 1", stats.Stubbed)
	}
	body, _ := os.ReadFile(dst)
	if strings.Contains(string(body), "outside the scope") || strings.Contains(string(body), "reasoning that led") {
		t.Fatal("a declined turn's essay or its thinking survived the filter")
	}
	if !strings.Contains(string(body), declineStub) {
		t.Fatal("the stub marker is not in the filtered copy")
	}
	// The clean turn passes through untouched.
	if !strings.Contains(string(body), "meanwhile, work that was done") {
		t.Fatal("a clean turn did not survive the filter")
	}
}

func TestClaudeFilterKeepsAFindingThatMerelySoundsLikeOne(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.jsonl")
	writeLines(t, src,
		claudeAssistant("I can't reproduce the 0.9 result on this exit; the budget is exhausted after 40 mints."))
	dst := filepath.Join(t.TempDir(), "dst.jsonl")
	stats, err := Claude(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Stubbed != 0 {
		t.Fatalf("a finding was stubbed as a decline (%d)", stats.Stubbed)
	}
}

func TestClaudeFilterCapsToolResultsAndCutsToTheBoundary(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.jsonl")
	huge := strings.Repeat("x", toolResultCap*3)
	writeLines(t, src,
		claudeUser("early work, before the compaction"),
		claudeAssistant("more early work"),
		`{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}`,
		claudeUser(`This session is being continued from a previous conversation.`),
		claudeUser([]map[string]any{{"type": "tool_result", "content": huge}}),
	)
	dst := filepath.Join(t.TempDir(), "dst.jsonl")
	stats, err := Claude(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Compacted {
		t.Fatal("the boundary cut did not report itself")
	}
	body, _ := os.ReadFile(dst)
	if strings.Contains(string(body), "early work") {
		t.Fatal("records before the boundary survived the cut")
	}
	if strings.Contains(string(body), huge) {
		t.Fatal("an oversized tool result survived the cap")
	}
	if !strings.Contains(string(body), "truncated") {
		t.Fatal("the truncation marker is missing")
	}
}

func TestClaudeFilterExplicitCutSkipsTheBoundary(t *testing.T) {
	// A summary written after the deviation point describes the drift, so a
	// rewind cuts to the deviation and leaves the boundary alone.
	src := filepath.Join(t.TempDir(), "src.jsonl")
	writeLines(t, src,
		claudeUser("on course: reproduce the baseline"),
		claudeAssistant("baseline reproduced, moving to the next item"),
		`{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}`,
		claudeUser(`This session is being continued from a previous conversation.`),
		claudeAssistant("drifted off into something the goal never asked for"),
	)
	cut, found, err := DeviationCut("claude", src, "baseline reproduced, moving to the next item")
	if err != nil {
		t.Fatal(err)
	}
	if !found || cut != 1 {
		t.Fatalf("deviation cut = %d, found %v; want line 1", cut, found)
	}
	dst := filepath.Join(t.TempDir(), "dst.jsonl")
	stats, err := Claude(src, dst, Options{KeepTo: &cut})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Compacted {
		t.Fatal("an explicit cut must not report the boundary cut it skipped")
	}
	body, _ := os.ReadFile(dst)
	// The summary pair that sits between the on-course turn and the drift
	// describes earlier, on-course work, so it passes through; the drift
	// after the deviation point does not.
	if strings.Contains(string(body), "drifted off") {
		t.Fatal("records after the deviation point survived the rewind")
	}
	if !strings.Contains(string(body), "baseline reproduced") {
		t.Fatal("the last on-course turn did not survive the rewind")
	}
}

func TestCodexFilterStubsAndCollapses(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.jsonl")
	said := func(text string, id string) string {
		rec := map[string]any{
			"type": "response_item",
			"payload": map[string]any{
				"type": "message", "role": "assistant", "id": id,
				"content": []map[string]any{{"type": "output_text", "text": text}},
			},
		}
		raw, _ := json.Marshal(rec)
		return string(raw)
	}
	writeLines(t, src,
		`{"type":"session_meta","payload":{"id":"meta"}}`,
		said(declEssay, "m1"),
		said("the actual work", "m2"),
		`{"type":"event_msg","payload":{"type":"agent_message","message":"echo one"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"echo two"}}`,
	)
	dst := filepath.Join(t.TempDir(), "dst.jsonl")
	stats, err := Codex(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Stubbed != 1 {
		t.Fatalf("stubbed %d, want 1", stats.Stubbed)
	}
	if stats.Dropped != 0 {
		t.Fatalf("rows the layout reader cannot compare were dropped: %d", stats.Dropped)
	}
	body, _ := os.ReadFile(dst)
	if strings.Contains(string(body), "outside the scope") {
		t.Fatal("a declined turn survived the codex filter")
	}
	if !strings.Contains(string(body), "the actual work") {
		t.Fatal("clean work did not survive the codex filter")
	}
}

func TestNoteNamesWhatWasRemoved(t *testing.T) {
	note := Stats{Dropped: 12, Stubbed: 2, Compacted: true}.Note()
	for _, want := range []string{"filtered", "compaction summary", "12", "2"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note %q missing %q", note, want)
		}
	}
	if !(Stats{}).Empty() {
		t.Fatal("an empty stats must report empty")
	}
}
