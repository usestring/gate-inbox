package search

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func openCodeToolMessage(tool, status, command, output string) string {
	data, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"content": []any{map[string]any{
			"type": "tool", "id": "call", "name": tool,
			"state": map[string]any{
				"status": status, "input": map[string]string{"command": command},
				"content": []any{map[string]string{"type": "text", "text": output}},
			},
		}},
	})
	return string(data)
}

func openCodeTextMessage(text string) string {
	data, _ := json.Marshal(map[string]any{
		"type":    "assistant",
		"content": []any{map[string]string{"type": "text", "text": text}},
	})
	return string(data)
}

func TestOpenCodePRCallsRequireCompletedBashCreation(t *testing.T) {
	const url = "https://github.com/example/repo/pull/1"
	for _, tc := range []struct {
		name, tool, status, command string
		want                        bool
	}{
		{"wrapper", "bash", "completed", "/skills/create-pr/scripts/gh-pr-create.sh --title fix", true},
		{"standalone", "bash", "completed", "gh pr create --fill", true},
		{"running", "bash", "running", "gh-pr-create.sh", false},
		{"failed", "bash", "error", "gh-pr-create.sh", false},
		{"list", "bash", "completed", "gh pr list", false},
		{"shell", "shell", "completed", "gh pr create --fill", true},
		{"skill", "skill", "completed", "gh-pr-create.sh", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s session
			s.recordCreations(opencodeMessageCalls("message", openCodeToolMessage(tc.tool, tc.status, tc.command, url)))
			if got := string(s.created); (got == url+"\n") != tc.want {
				t.Fatalf("created = %q, want creation %v", got, tc.want)
			}
		})
	}
	if calls := opencodeMessageCalls("prose", openCodeTextMessage("gh-pr-create.sh https://github.com/example/repo/pull/1")); len(calls) != 0 {
		t.Fatalf("prose produced tool calls: %+v", calls)
	}
}

func TestOpenCodePRCompletionUpdatesAreDiscoveredOnce(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE session_v2 (id TEXT PRIMARY KEY);
		CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, sessionID, data string, at int64) {
		t.Helper()
		if _, err := db.Exec("INSERT INTO session_message VALUES (?, ?, 'assistant', 0, ?, ?, ?)", id, sessionID, at, at, data); err != nil {
			t.Fatal(err)
		}
	}
	update := func(id, data string, at int64) {
		t.Helper()
		if _, err := db.Exec("UPDATE session_message SET data=?, time_updated=? WHERE id=?", data, at, id); err != nil {
			t.Fatal(err)
		}
	}
	const url1 = "https://github.com/example/repo/pull/1"
	const url2 = "https://github.com/example/repo/pull/2"
	const url3 = "https://github.com/example/repo/pull/3"
	const command = "gh-pr-create.sh --title fix"
	insert("running", "oc", openCodeToolMessage("bash", "running", command, ""), 100)
	insert("newer", "oc", openCodeTextMessage("waiting for completion"), 200)
	insert("foreign", "other", openCodeToolMessage("bash", "completed", command, url3), 400)
	x := openTest(t, Options{OpenCodeDB: path, Limits: Limits{Text: 1024, Input: 1024, Result: 32}})
	targets := []Target{{Key: "board", Tool: ToolOpenCode, AgentID: "oc"}}
	refreshAll(t, x, targets)
	if got := x.Created("board"); got != "" {
		t.Fatalf("premature creation: %q", got)
	}

	output := "completion-marker\n" + strings.Repeat("validation output\n", 100) + url1
	update("running", openCodeToolMessage("bash", "completed", command, output), 300)
	refreshAll(t, x, targets)
	if got := x.Created("board"); got != url1+"\n" {
		t.Fatalf("late completion: %q", got)
	}
	if got := hits(t, x, "completion-marker"); len(got) != 1 {
		t.Fatalf("completion not searchable: %v", got)
	}

	insert("a-same-millisecond", "oc", openCodeToolMessage("bash", "completed", command, url2), 300)
	refreshAll(t, x, targets)
	if got := x.Created("board"); got != url1+"\n"+url2+"\n" {
		t.Fatalf("same-millisecond insertion: %q", got)
	}
	insert("z-same-millisecond", "oc", openCodeToolMessage("bash", "running", command, ""), 300)
	refreshAll(t, x, targets)
	update("z-same-millisecond", openCodeToolMessage("bash", "completed", command, url3), 300)
	refreshAll(t, x, targets)
	if got := x.Created("board"); got != url1+"\n"+url2+"\n"+url3+"\n" {
		t.Fatalf("same-millisecond completion: %q", got)
	}

	update("running", openCodeToolMessage("bash", "completed", command, output+"\nmetadata refreshed"), 500)
	refreshAll(t, x, targets)
	if got := x.Created("board"); got != url1+"\n"+url2+"\n"+url3+"\n" {
		t.Fatalf("repeated completion duplicated PR: %q", got)
	}
	insert("unrelated", "other", openCodeTextMessage("unrelated write"), 600)
	if p := refreshAll(t, x, targets); p.Changed || p.Rows != 0 {
		t.Fatalf("unchanged boundary reindexed: %+v", p)
	}
	if p := refreshAll(t, x, targets); p.Changed {
		t.Fatalf("idle refresh changed index: %+v", p)
	}

	refreshAll(t, x, []Target{{Key: "board", Tool: ToolOpenCode, AgentID: "other"}})
	if got := x.Created("board"); got != url3+"\n" {
		t.Fatalf("conversation switch retained old PRs in index: %q", got)
	}
}
