package ui

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// Feature parity across the three tools the index reads, end to end: one
// board row per tool, each carrying its tool's own conversation id and
// pointing at a transcript in that tool's own layout — a Claude JSONL under
// CLAUDE_CONFIG_DIR, a Codex rollout under CODEX_HOME, an opencode session
// in a database — all resolved by the same poll pass, indexed by the same
// refresher, and each found by a query that only its transcript contains,
// with the ≡hist badge. Each transcript holds the same three kinds of
// content, prose, a command and a tool result, so no tool is searchable
// on fewer kinds than another.
func TestHistorySearchAllToolsEndToEnd(t *testing.T) {
	m := buildModel(t)
	claudeHome := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	t.Setenv("CODEX_HOME", codexHome)
	dir := t.TempDir()

	// Rows straight into the store, as park/unpark and adoption create them:
	// the history path needs a row with a conversation id, not a pane.
	rows := []store.Session{
		{ID: "row-claude", Name: "claude-worker", Tool: "claude", Cwd: dir, AgentSessionID: "0d3c8e1a-e2e0-4c0f-9c4d-00000000c1a0", Status: "idle"},
		{ID: "row-codex", Name: "codex-worker", Tool: "codex", Cwd: dir, AgentSessionID: "0d3c8e1a-e2e0-4c0f-9c4d-0000000c0de0", Status: "idle"},
		{ID: "row-opencode", Name: "opencode-worker", Tool: "opencode", Cwd: dir, AgentSessionID: "ses_e2e0opencode", Status: "idle"},
	}
	for _, row := range rows {
		row.CreatedAt = time.Now()
		if err := m.store.CreateSession(row); err != nil {
			t.Fatal(err)
		}
	}

	// Claude: JSONL under projects/.
	claudePath := filepath.Join(claudeHome, "projects", "-e2e", rows[0].AgentSessionID+".jsonl")
	mustWriteLines(t, claudePath,
		jsonLine(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "claude prose: rotate the r2 token"}}),
		jsonLine(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "secrets-cli set BACKUP_BUCKET_KEY"}},
		}}}),
		jsonLine(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t", "content": "claude result: r2-rotation-ok-7f3a"},
		}}}),
	)
	// Codex: a rollout named by timestamp and id under sessions/YYYY/MM/DD.
	codexPath := filepath.Join(codexHome, "sessions", "2026", "09", "05", "rollout-2026-09-05T10-00-00-"+rows[1].AgentSessionID+".jsonl")
	mustWriteLines(t, codexPath,
		jsonLine(map[string]any{"type": "session_meta", "payload": map[string]any{"id": rows[1].AgentSessionID, "cwd": dir}}),
		jsonLine(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "codex prose: port the brain to codex"},
		}}}),
		jsonLine(map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "shell", "arguments": `{"command":["go","build","./cmd/brain-codex"]}`}}),
		jsonLine(map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "c", "output": "codex result: brain-build-ok-9b2e"}}),
	)
	// opencode: messages in its database.
	opencodeDB := filepath.Join(tmuxtest.ScratchDir(t), "opencode.db")
	db, err := sql.Open("sqlite", opencodeDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session_v2 (id TEXT PRIMARY KEY);
		CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i, message := range []struct{ kind, data string }{
		{"user", `{"type":"user","text":"opencode prose: roll the portal deploy"}`},
		{"assistant", `{"type":"assistant","content":[{"type":"tool","name":"bash","state":{"status":"completed","input":{"command":"kubectl rollout restart deploy/portal-e2e"},"content":[{"type":"text","text":"opencode result: portal-rolled-ok-4c1d"}]}}]}`},
		{"assistant", `{"type":"assistant","content":[{"type":"tool","name":"edit","state":{"status":"completed","input":{"filePath":"/repo/portal/app/page-e2e.tsx"}}}]}`},
	} {
		if _, err := db.Exec("INSERT INTO session_message VALUES (?, ?, ?, 0, ?, ?, ?)", fmt.Sprintf("p%d", i), rows[2].AgentSessionID, message.kind, 100+i, 100+i, message.data); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	m.history = search.New(search.Options{OpenCodeDB: opencodeDB})
	m.poller.locator = newHistoryLocator()
	m.poller.historyFormats = historyToolFormats(m.cfg)
	msgs := make(chan tea.Msg, 16)
	go m.runHistoryIndex(func(msg tea.Msg) { msgs <- msg })

	m.applyCmd(t, m.refreshCmd())
	deadline := time.After(20 * time.Second)
	for m.history.Stats().Sources < 3 {
		select {
		case msg := <-msgs:
			m.Update(msg)
		case <-deadline:
			t.Fatalf("the refresher indexed %+v; want all three tools", m.history.Stats())
		}
	}
	if st := m.history.Stats(); st.Turns != 10 {
		t.Fatalf("index holds %+v; want every prose, tool-name, command, result and patch line across the three tools", st)
	}

	// Each tool is found by its prose, its command, its result, and (where
	// the tool records them) its edited files — by a term no other row has.
	for _, c := range []struct{ query, row string }{
		{"rotate the r2 token", "claude-worker"},
		{"BACKUP_BUCKET_KEY", "claude-worker"},
		{"r2-rotation-ok-7f3a", "claude-worker"},
		{"port the brain to codex", "codex-worker"},
		{"./cmd/brain-codex", "codex-worker"},
		{"brain-build-ok-9b2e", "codex-worker"},
		{"roll the portal deploy", "opencode-worker"},
		{"deploy/portal-e2e", "opencode-worker"},
		{"portal-rolled-ok-4c1d", "opencode-worker"},
		{"page-e2e.tsx", "opencode-worker"},
		{"*-ok-*", "claude-worker codex-worker opencode-worker"},
	} {
		m.searching, m.search = true, c.query
		m.rebuildRows()
		if got := sessionNames(m); len(got) != 0 && c.query != "*-ok-*" {
			t.Fatalf("%q matched %v before the index answered", c.query, got)
		}
		_, cmd := m.Update(m.scheduleHistorySearch()())
		m.applyCmd(t, cmd)
		names := sessionNames(m)
		sort.Strings(names)
		if got := strings.Join(names, " "); got != c.row {
			t.Fatalf("%q: rows %q, want %q", c.query, got, c.row)
		}
		rowsText := railText(t, m)
		for _, name := range strings.Fields(c.row) {
			if line := rowsText[lineWith(t, rowsText, name)]; !strings.Contains(line, "≡hist") {
				t.Fatalf("%q: %s should carry the transcript badge: %q", c.query, name, line)
			}
		}
	}
}

func jsonLine(v any) string {
	line, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(line)
}

func mustWriteLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
