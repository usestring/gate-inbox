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

// A page is the session's when its own call to Claude Code's Artifact tool published it, with or
// without the action spelled out. Reading, listing or uploading an asset to an artifact is not
// publishing one, and a page address in prose or in some other command's output is a mention.
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
		toolResultLine("cat", "https://claude.ai/artifact/Catted1"),
		// An extension's publish tool is not read from the transcript: the extension attaches
		// its pages to the session itself.
		namedToolUseLine("ext", "mcp__pages__publish_page", map[string]any{"title": "Ext", "file_path": "/tmp/s/ext.html"}),
		toolResultLine("ext", "published https://claude.ai/artifact/Ext1"),
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
		namedToolUseLine("p3", "Artifact", map[string]any{"action": "publish", "title": "Intro Call", "file_path": "/tmp/s/call.html"}),
		toolResultLine("p3", "Published /tmp/s/call.html at https://claude.ai/code/artifact/0f6c2b1e-1a2b-4c3d-9e8f-001122334455"),
	)
	refreshAll(t, x, targets)
	want := "https://claude.ai/artifact/Mine1\tcourt-title-check\n" +
		"https://claude.ai/code/artifact/0f6c2b1e-1a2b-4c3d-9e8f-001122334455\tIntro Call\n"
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
