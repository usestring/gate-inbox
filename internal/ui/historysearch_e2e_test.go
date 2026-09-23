package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/search"
)

// End to end, with nothing stubbed: a real tmux session on the board whose
// row carries a conversation id, a transcript for that id on disk under a
// Claude home of the test's own, a real poll pass resolving the row to the
// file, the refresher goroutine indexing it and reporting back, and a query
// typed into the filter the way a keystroke arrives — debounce, off-thread
// answer, rail redraw — surfacing the row with its ≡hist badge.
func TestHistorySearchEndToEnd(t *testing.T) {
	m := buildModel(t)
	claudeHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	dir := t.TempDir()
	createSession(t, m, "hist-e2e", dir, "")
	createSession(t, m, "bystander", dir, "")
	target := sessionNamed(t, m, "hist-e2e")

	const agentID = "0d3c8e1a-e2e0-4c0f-9c4d-000000000001"
	const needle = "pgbackrest stanza check failed on db-01"
	transcript := filepath.Join(claudeHome, "projects", "-some-project", agentID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "yesterday " + needle + ", twice"}})
	if err := os.WriteFile(transcript, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetAgentSessionID(target.ID, agentID); err != nil {
		t.Fatal(err)
	}

	// What StartPoller wires, with the poll pass driven synchronously and
	// the refresher left to run for real.
	m.history = search.New(search.Options{})
	m.poller.locator = newHistoryLocator()
	m.poller.historyFormats = historyToolFormats(m.cfg)
	msgs := make(chan tea.Msg, 16)
	go m.runHistoryIndex(func(msg tea.Msg) { msgs <- msg })

	m.applyCmd(t, m.refreshCmd())
	select {
	case msg := <-msgs:
		indexed, ok := msg.(historyIndexedMsg)
		if !ok || !indexed.progress.Changed || indexed.progress.Rows == 0 {
			t.Fatalf("refresher sent %T %+v", msg, msg)
		}
		m.Update(msg)
	case <-time.After(15 * time.Second):
		t.Fatal("the refresher never indexed the transcript")
	}
	if st := m.history.Stats(); st.Sources != 1 || st.Turns != 1 {
		t.Fatalf("index holds %+v; want the one row with a transcript", st)
	}

	// The query is nowhere the rows print or show; only the transcript has it.
	m.searching = true
	m.search = "Stanza CHECK failed"
	m.rebuildRows()
	if got := sessionNames(m); len(got) != 0 {
		t.Fatalf("metadata and pane text alone matched %v", got)
	}
	debounce := m.scheduleHistorySearch()
	if debounce == nil {
		t.Fatal("no debounce armed for a query the index can answer")
	}
	_, cmd := m.Update(debounce())
	if cmd == nil {
		t.Fatal("the debounce did not ask the index")
	}
	m.applyCmd(t, cmd)
	if got := sessionNames(m); len(got) != 1 || got[0] != "hist-e2e" {
		t.Fatalf("filter with history = %v want [hist-e2e]", got)
	}
	rows := railText(t, m)
	if row := rows[lineWith(t, rows, "hist-e2e")]; !strings.Contains(row, "≡hist") {
		t.Fatalf("the row should say the hit was in its transcript: %q", row)
	}

	// The session keeps talking: the appended turn is found without a
	// keystroke, through the refresher's own report.
	more, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "pgbackrest --stanza=main check"}},
	}}})
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(append(more, '\n'))
	f.Close()
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(transcript, later, later)
	m.search = "--stanza=main"
	m.rebuildRows()
	select {
	case msg := <-msgs:
		_, reask := m.Update(msg)
		if reask == nil {
			t.Fatal("an index change with a query on screen must re-ask")
		}
		m.applyCmd(t, reask)
	case <-time.After(15 * time.Second):
		t.Fatal("the refresher never picked up the appended turn")
	}
	if got := sessionNames(m); len(got) != 1 || got[0] != "hist-e2e" {
		t.Fatalf("after the append: %v", got)
	}
}
