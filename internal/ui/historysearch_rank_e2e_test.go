package ui

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

// Relevance, end to end and with nothing stubbed. Six rows carry the same
// term in six different ways — in a row's own name, on a live pane's
// screen, typed by the operator just now, typed by the operator twenty
// turns ago, printed by a tool three times, printed by a tool once — across
// the three transcript layouts, and a seventh never mentions it. One poll
// pass, the real refresher, one typed query, and the rail must come back
// in relevance order with the right badges:
//
//	named       the query is in what the row prints — no badge
//	screen      the query is on the pane — ≡pane
//	typed-now   typed in the newest turn, weight 1 at no distance
//	chatty      three tool outputs in the newest turns, 3 × 0.3, decayed
//	typed-ago   typed once, twenty turns back, weight 1 halved
//	printed     one tool output in the newest turn, weight 0.3
func TestHistorySearchRankingEndToEnd(t *testing.T) {
	m := buildModel(t)
	claudeHome := t.TempDir()
	codexHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	t.Setenv("CODEX_HOME", codexHome)
	dir := t.TempDir()
	const term = "pgbackrest"

	// The screen row is a real pane showing the term; the named row's name
	// carries it. Neither has a transcript.
	createSession(t, m, "screen", dir, "")
	createSession(t, m, "named-"+term, dir, "")
	screen := sessionNamed(t, m, "screen")
	if err := m.tmux.SendText(screen.ID, "fatal: "+term+" stanza missing"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	waitForPaneText(t, m, screen.ID, term)

	ids := map[string]string{
		"typed-now": "0d3c8e1a-e2e0-4c0f-9c4d-00000000a001",
		"typed-ago": "0d3c8e1a-e2e0-4c0f-9c4d-00000000a002",
		"chatty":    "ses_e2e_chatty",
		"printed":   "0d3c8e1a-e2e0-4c0f-9c4d-00000000a004",
		"bystander": "0d3c8e1a-e2e0-4c0f-9c4d-00000000a005",
	}
	tools := map[string]string{"typed-now": "claude", "typed-ago": "claude", "chatty": "opencode", "printed": "codex", "bystander": "claude"}
	for _, name := range []string{"typed-now", "typed-ago", "chatty", "printed", "bystander"} {
		if err := m.store.CreateSession(store.Session{ID: "row-" + name, Name: name, Tool: tools[name], Cwd: dir, AgentSessionID: ids[name], Status: "idle", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}

	// Claude: the term typed in the newest turn.
	mustWriteLines(t, filepath.Join(claudeHome, "projects", "-e2e", ids["typed-now"]+".jsonl"),
		jsonLine(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "unrelated opener"}}),
		jsonLine(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "why did " + term + " fail on db-01"}}),
	)
	// Claude: the term typed once, then twenty unrelated turns.
	lines := []string{jsonLine(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "check " + term + " please"}})}
	for i := 0; i < 20; i++ {
		lines = append(lines, jsonLine(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": fmt.Sprintf("unrelated turn %02d", i)}}))
	}
	mustWriteLines(t, filepath.Join(claudeHome, "projects", "-e2e", ids["typed-ago"]+".jsonl"), lines...)
	// Claude: never mentions it.
	mustWriteLines(t, filepath.Join(claudeHome, "projects", "-e2e", ids["bystander"]+".jsonl"),
		jsonLine(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "nothing to see"}}),
	)
	// Codex: the term in one tool output, newest.
	mustWriteLines(t, filepath.Join(codexHome, "sessions", "2026", "09", "05", "rollout-2026-09-05T10-00-00-"+ids["printed"]+".jsonl"),
		jsonLine(map[string]any{"type": "session_meta", "payload": map[string]any{"id": ids["printed"], "cwd": dir}}),
		jsonLine(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "run the backup check"}}}}),
		jsonLine(map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "c", "output": term + ": stanza ok"}}),
	)
	// opencode: the term in three tool outputs, newest.
	opencodeDB := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", opencodeDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session_v2 (id TEXT PRIMARY KEY);
		CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	shell := func(command, output string) string {
		return `{"type":"assistant","content":[{"type":"tool","name":"bash","state":{"status":"completed","input":{"command":"` + command + `"},"content":[{"type":"text","text":"` + output + `"}]}}]}`
	}
	messages := []struct{ kind, data string }{
		{"user", `{"type":"user","text":"look at the backups"}`},
		{"assistant", shell("ls /var/lib", term+" repo 1")},
		{"assistant", shell("ls /var/log", term+" repo 2")},
		{"assistant", shell("ls /tmp", term+" repo 3")},
	}
	for i, message := range messages {
		if _, err := db.Exec("INSERT INTO session_message VALUES (?, ?, ?, 0, ?, ?, ?)", fmt.Sprintf("p%d", i), "ses_e2e_chatty", message.kind, 100+i, 100+i, message.data); err != nil {
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
	for m.history.Stats().Sources < 5 {
		select {
		case msg := <-msgs:
			m.Update(msg)
		case <-deadline:
			t.Fatalf("the refresher indexed %+v; want five transcripts", m.history.Stats())
		}
	}

	m.searching, m.search = true, term
	m.rebuildRows()
	_, cmd := m.Update(m.scheduleHistorySearch()())
	m.applyCmd(t, cmd)

	want := []string{"named-" + term, "screen", "typed-now", "chatty", "typed-ago", "printed"}
	if got := sessionNames(m); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("rail = %v\nwant  %v", got, want)
	}
	rows := railText(t, m)
	badges := map[string]string{
		"named-" + term: "",
		"screen":        "≡pane",
		"typed-now":     "≡hist",
		"chatty":        "≡hist·3",
		"typed-ago":     "≡hist",
		"printed":       "≡hist",
	}
	for name, badge := range badges {
		line := rows[lineWith(t, rows, name)]
		if badge == "" {
			if strings.Contains(line, "≡") {
				t.Fatalf("%s carries a badge it should not: %q", name, line)
			}
			continue
		}
		if !strings.Contains(line, badge) {
			t.Fatalf("%s should carry %s: %q", name, badge, line)
		}
		if badge == "≡hist" && strings.Contains(line, "≡hist·") {
			t.Fatalf("%s should carry no count: %q", name, line)
		}
	}

	// The operator says it once more in the stale session: it jumps ahead
	// of the tool outputs on the next refresh, without a keystroke.
	// The cold build may have reported more than once; those reports are
	// about the past. Only reports after the append carry the new turn.
	for drained := false; !drained; {
		select {
		case <-msgs:
		default:
			drained = true
		}
	}
	f := filepath.Join(claudeHome, "projects", "-e2e", ids["typed-ago"]+".jsonl")
	appendLine(t, f, jsonLine(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": term + " again, now"}}))
	want = []string{"named-" + term, "screen", "typed-ago", "typed-now", "chatty", "printed"}
	deadline = time.After(20 * time.Second)
	for strings.Join(sessionNames(m), " ") != strings.Join(want, " ") {
		select {
		case msg := <-msgs:
			_, reask := m.Update(msg)
			if reask == nil {
				t.Fatal("an index change with a query on screen must re-ask")
			}
			m.applyCmd(t, reask)
		case <-deadline:
			t.Fatalf("rail after the append = %v\nwant  %v", sessionNames(m), want)
		}
	}
}

// appendLine adds one transcript line and moves the file's mtime forward,
// since a same-second append can look unchanged to the refresher's stat.
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, later, later)
}
