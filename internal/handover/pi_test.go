package handover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// piMsg is one pi message entry, shaped as pi 1.5.1 writes them.
func piMsg(id, parent, role string, content any) string {
	entry := map[string]any{
		"type": "message", "id": id, "timestamp": "2026-11-10T11:00:00.000Z",
		"message": map[string]any{"role": role, "content": content, "timestamp": 1},
	}
	if parent == "" {
		entry["parentId"] = nil
	} else {
		entry["parentId"] = parent
	}
	raw, _ := json.Marshal(entry)
	return string(raw)
}

func piText(text string) []map[string]any { return []map[string]any{{"type": "text", "text": text}} }

const piHeader = `{"type":"session","version":3,"id":"bbbbbbbb-cccc-4444-8888-dddddddddddd","timestamp":"2026-11-10T11:00:37.733Z","cwd":"/srv/project"}`

func piEntryIDs(t *testing.T, lines []string) []string {
	t.Helper()
	var ids []string
	for _, line := range lines {
		var entry struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("output line is not JSON: %v\n%s", err, line)
		}
		if entry.Type == "session" {
			ids = append(ids, "header")
		} else {
			ids = append(ids, entry.ID)
		}
	}
	return ids
}

// Only the branch pi would resume survives: the leaf is the last entry, and
// an abandoned branch from /tree is dropped with what it said.
func TestPiKeepsTheActiveBranch(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "s.jsonl"), filepath.Join(dir, "s.handover.jsonl")
	writeLines(t, src,
		piHeader,
		piMsg("u1", "", "user", piText("fix the build")),
		piMsg("a1", "u1", "assistant", piText("trying approach A")),
		piMsg("u2", "a1", "user", piText("abandoned branch prompt")),
		piMsg("a2", "u2", "assistant", piText("approach A went nowhere")),
		`{"type":"branch_summary","id":"b1","parentId":"a1","timestamp":"2026-11-10T11:00:00.000Z","fromId":"a2","summary":"A failed"}`,
		piMsg("u3", "b1", "user", piText("try approach B")),
		piMsg("a3", "u3", "assistant", piText("approach B works")),
	)
	stats, err := Pi(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(piEntryIDs(t, readLines(t, dst)), " ")
	if got != "header u1 a1 b1 u3 a3" {
		t.Errorf("kept %s, want header u1 a1 b1 u3 a3", got)
	}
	if stats.Kept != 6 || !stats.Empty() {
		t.Errorf("stats = %+v", stats)
	}
}

// The latest compaction on the path stands in for what it summarised: the
// copy keeps it, the entries from firstKeptEntryId, and what follows.
func TestPiCutsToTheLastCompaction(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "s.jsonl"), filepath.Join(dir, "s.handover.jsonl")
	writeLines(t, src,
		piHeader,
		piMsg("u1", "", "user", piText("old prompt")),
		piMsg("a1", "u1", "assistant", piText("old reply")),
		piMsg("u2", "a1", "user", piText("kept prompt")),
		piMsg("a2", "u2", "assistant", piText("kept reply")),
		`{"type":"compaction","id":"c1","parentId":"a2","timestamp":"2026-11-10T11:00:00.000Z","summary":"earlier work","firstKeptEntryId":"u2","tokensBefore":5000}`,
		piMsg("u3", "c1", "user", piText("after compaction")),
	)
	stats, err := Pi(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(piEntryIDs(t, readLines(t, dst)), " "); got != "header u2 a2 c1 u3" {
		t.Errorf("kept %s, want header u2 a2 c1 u3", got)
	}
	if !stats.Compacted {
		t.Error("Compacted not reported")
	}
}

// A repeated turn collapses into its first, and the entry after it takes the
// dropped entry's parent so the copy stays one path; a declining turn is
// stubbed; a huge tool result is cut.
func TestPiCollapsesLoopsStubsDeclinesAndCapsResults(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "s.jsonl"), filepath.Join(dir, "s.handover.jsonl")
	call := []map[string]any{{"type": "toolCall", "id": "toolu_1", "name": "bash", "arguments": map[string]any{"command": "make"}}}
	call2 := []map[string]any{{"type": "toolCall", "id": "toolu_2", "name": "bash", "arguments": map[string]any{"command": "make"}}}
	huge := strings.Repeat("x", piResultCap*2)
	writeLines(t, src,
		piHeader,
		piMsg("u1", "", "user", piText("build it")),
		piMsg("a1", "u1", "assistant", call),
		piMsg("r1", "a1", "toolResult", piText(huge)),
		piMsg("a2", "r1", "assistant", call2),
		piMsg("r2", "a2", "toolResult", piText(huge)),
		piMsg("a3", "r2", "assistant", piText(declEssay)),
	)
	stats, err := Pi(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, dst)
	if got := strings.Join(piEntryIDs(t, lines), " "); got != "header u1 a1 r1 a3" {
		t.Fatalf("kept %s, want header u1 a1 r1 a3", got)
	}
	if stats.Dropped != 2 || stats.Stubbed != 1 {
		t.Errorf("stats = %+v", stats)
	}
	var last struct {
		ParentID string `json:"parentId"`
		Message  struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[4]), &last); err != nil {
		t.Fatal(err)
	}
	if last.ParentID != "r1" {
		t.Errorf("a3 parent = %q, want r1 (the dropped entries' surviving ancestor)", last.ParentID)
	}
	if last.Message.Content[0].Text != declineStub {
		t.Errorf("decline not stubbed: %q", last.Message.Content[0].Text)
	}
	if len(lines[3]) > piResultCap+1024 || !strings.Contains(lines[3], "truncated") {
		t.Errorf("tool result not capped (%d bytes)", len(lines[3]))
	}
}

// The rewind: the deviation quote names the last on-course turn, and the copy
// ends there with it as the leaf.
func TestPiRewindCutsAtTheDeviation(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "s.jsonl"), filepath.Join(dir, "s.rewind.jsonl")
	writeLines(t, src,
		piHeader,
		piMsg("u1", "", "user", piText("migrate the schema")),
		piMsg("a1", "u1", "assistant", piText("Schema migrated and tests pass.")),
		piMsg("u2", "a1", "user", piText("now refactor everything")),
		piMsg("a2", "u2", "assistant", piText("Rewriting the whole repository.")),
	)
	cut, found, err := DeviationCut("pi", src, "schema migrated and   tests pass")
	if err != nil || !found || cut != 2 {
		t.Fatalf("DeviationCut = %d %v %v, want line 2", cut, found, err)
	}
	if _, err := Pi(src, dst, Options{KeepTo: &cut}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(piEntryIDs(t, readLines(t, dst)), " "); got != "header u1 a1" {
		t.Errorf("kept %s, want header u1 a1", got)
	}
}

// The session pi 1.5.1 wrote on the rig passes through whole: one branch, no
// loop, no decline, so nothing is removed and the copy is the conversation.
func TestPiRealSessionPassesThrough(t *testing.T) {
	src := filepath.Join("..", "convo", "testdata", "pi-1.5.1", "named.jsonl")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "s.handover.jsonl")
	stats, err := Pi(src, dst, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Empty() || stats.Kept != strings.Count(string(raw), "\n") {
		t.Errorf("stats = %+v over %d lines", stats, strings.Count(string(raw), "\n"))
	}
	if !Reads("pi") || Reads("opencode") {
		t.Error("Reads disagrees with the layouts the filter reads")
	}
}
