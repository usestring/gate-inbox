package search

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func namedToolUseLine(id, name string, input map[string]any) string {
	line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": id, "name": name, "input": input},
	}}})
	return string(line)
}

// A page is the session's when its own publish call returned it: the Artifact tool publishing (with
// or without the action spelled out) and an MCP server's publish_artifact. Reading, listing or
// uploading an asset to an artifact is not publishing one, and a page address in prose or in some
// other command's output is a mention.
func TestPublishedPagesComeFromThePublishingCallAndItsResult(t *testing.T) {
	x := openTest(t, Options{})
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path,
		userLine("see https://claude.ai/artifact/Mentioned1 for context"),
		namedToolUseLine("read", "Artifact", map[string]any{"action": "read", "url": "https://claude.ai/artifact/Read1"}),
		toolResultLine("read", "<html>https://claude.ai/artifact/Read1</html>"),
		namedToolUseLine("list", "Artifact", map[string]any{"action": "list"}),
		toolResultLine("list", "Report https://claude.ai/artifact/Listed1"),
		namedToolUseLine("asset", "Artifact", map[string]any{"url": "https://claude.ai/artifact/Mine1", "asset": true, "file_path": "/x/logo.png"}),
		toolResultLine("asset", "uploaded to https://claude.ai/artifact/Mine1"),
		toolUseLine("cat", "cat notes.md"),
		toolResultLine("cat", "https://artifacts.example.test/a/art_Other1"),
		namedToolUseLine("p1", "Artifact", map[string]any{"file_path": "/tmp/s/court-title-check.html", "icon": "document"}),
	)
	targets := []Target{{Key: "k", Tool: ToolClaude, Path: path}}
	refreshAll(t, x, targets)
	if got := x.Published("k"); got != "" {
		t.Fatalf("nothing has come back from a publish yet, got %q", got)
	}

	// The result lands in a later append, as it does while a session streams.
	writeLines(t, path,
		toolResultLine("p1", "Published /tmp/s/court-title-check.html at https://claude.ai/artifact/Mine1 (Version 1)"),
		namedToolUseLine("p2", "Artifact", map[string]any{"action": "publish", "file_path": "/tmp/s/court-title-check.html"}),
		toolResultLine("p2", "Published /tmp/s/court-title-check.html at https://claude.ai/artifact/Mine1 (Version 2)"),
		namedToolUseLine("p3", "mcp__gate-inbox__publish_artifact", map[string]any{"title": "Intro Call", "content_path": "/tmp/s/call.html"}),
		toolResultLine("p3", `{"id":"art_rEhw-1","title":"Intro Call","url":"https://artifacts.example.test/a/art_rEhw-1","public_url":null}`),
	)
	refreshAll(t, x, targets)
	want := "https://claude.ai/artifact/Mine1\tcourt-title-check\n" +
		"https://artifacts.example.test/a/art_rEhw-1\tIntro Call\n"
	if got := x.Published("k"); got != want {
		t.Errorf("published = %q, want %q", got, want)
	}
	if got := x.Created("k"); got != "" {
		t.Errorf("a publish result was read as a pull request: %q", got)
	}
	if x.Published("absent") != "" {
		t.Error("an unknown key has published nothing")
	}
}

// Codex names an MCP tool in the function call, and its output is paired the same way.
func TestCodexPublishedPages(t *testing.T) {
	x := openTest(t, Options{})
	path := filepath.Join(t.TempDir(), "rollout-2026-09-04T10-00-00-abc.jsonl")
	call, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "function_call", "name": "mcp__gate-inbox__publish_artifact", "call_id": "c1",
		"arguments": `{"title":"Fleet audit","content_path":"/tmp/a.html"}`,
	}})
	out, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "function_call_output", "call_id": "c1", "output": `{"url":"https://artifacts.example.test/a/art_Codex1"}`,
	}})
	other, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "function_call_output", "call_id": "c2", "output": "https://artifacts.example.test/a/art_Other1",
	}})
	writeLines(t, path, string(call), string(out), string(other))
	refreshAll(t, x, []Target{{Key: "k", Tool: ToolCodex, Path: path}})
	if got, want := x.Published("k"), "https://artifacts.example.test/a/art_Codex1\tFleet audit\n"; got != want {
		t.Errorf("published = %q, want %q", got, want)
	}
}
