package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Every configured tool resolves to the reader its transcript needs, by
// session_store, else by the binary it runs, else by its own name.
func TestHistoryToolFormatsFollowTheConfig(t *testing.T) {
	cfg := config.Config{Tools: map[string]config.Tool{
		"claude":   {Command: "/opt/wrappers/claude-with-flags"},
		"cc":       {Command: "/usr/local/bin/claude --dangerously-skip-permissions"},
		"codex":    {Command: "codex", SessionStore: "codex"},
		"opencode": {Command: "opencode", SessionStore: "opencode"},
		"hermes":   {Command: "hermes --cli", SessionStore: "hermes"},
		"terminal": {Command: ""},
	}}
	got := historyToolFormats(cfg)
	want := map[string]string{"claude": "claude", "cc": "claude", "codex": "codex", "opencode": "opencode", "hermes": "hermes", "terminal": "terminal"}
	for name, format := range want {
		if got[name] != format {
			t.Errorf("%s -> %q want %q", name, got[name], format)
		}
	}
}

// A model over three rows and a real index in a temp dir: one row said the
// needle in its transcript hours ago, one shows it on screen, one has it in
// its name. The filter must keep all three and the badge must say where
// each hit was.
func historyModel(t *testing.T) (*Model, *search.Index, string) {
	t.Helper()
	m := &Model{width: 120, height: 30, collapsed: map[string]bool{}}
	m.sessions = []store.Session{
		{ID: "1", Name: "db-primary-07-migration", Tool: "claude", Status: status.Idle},
		{ID: "2", Name: "web-ui", Tool: "claude", Status: status.Idle},
		{ID: "3", Name: "quiet", Tool: "claude", Status: status.Idle},
		{ID: "4", Name: "bystander", Tool: "claude", Status: status.Idle},
	}
	m.searchText = map[string]string{"2": "fatal: could not resolve db-primary-07"}

	dir := t.TempDir()
	transcript := filepath.Join(dir, "quiet.jsonl")
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "yesterday we lost db-primary-07 for an hour"}})
	if err := os.WriteFile(transcript, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	index := search.New(search.Options{})
	t.Cleanup(func() { index.Close() })
	for {
		p, err := index.Refresh([]search.Target{{Key: "3", Tool: search.ToolClaude, Path: transcript, AgentID: "quiet"}}, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if p.Done {
			break
		}
	}
	m.history = index
	return m, index, transcript
}

// runHistoryQuery drives a keystroke's path by hand: arm, fire the debounce
// with its own seq, run the query command, apply the answer.
func runHistoryQuery(t *testing.T, m *Model, query string) {
	t.Helper()
	m.searching, m.search = true, query
	m.rebuildRows()
	cmd := m.scheduleHistorySearch()
	if cmd == nil {
		return
	}
	// The debounce is a Tick; its message names the seq it was armed with.
	m.applyCmd(t, m.historySearchCmd())
}

func TestHistorySearchFindsWhatASessionSaidAndBadgesIt(t *testing.T) {
	m, _, _ := historyModel(t)
	const needle = "db-primary-07"

	if got := filterFor(m, needle); len(got) != 2 {
		t.Fatalf("before the index answers the filter has metadata and pane alone: %v", got)
	}
	runHistoryQuery(t, m, needle)
	got := sessionNames(m)
	if len(got) != 3 || got[0] != "db-primary-07-migration" || got[1] != "web-ui" || got[2] != "quiet" {
		t.Fatalf("filter with history = %v want [db-primary-07-migration web-ui quiet]", got)
	}
	rows := railTextAt(m, 70)
	if row := rows[lineWith(t, rows, "migration")]; strings.Contains(row, "≡") {
		t.Fatalf("a row whose name carries the query needs no badge: %q", row)
	}
	if row := rows[lineWith(t, rows, "web-ui")]; !strings.Contains(row, "≡pane") || strings.Contains(row, "≡hist") {
		t.Fatalf("a pane hit keeps the pane badge: %q", row)
	}
	if row := rows[lineWith(t, rows, "quiet")]; !strings.Contains(row, "≡hist") {
		t.Fatalf("a row the query is only in the transcript of should say so: %q", row)
	}
}

// A late answer to an earlier query must not apply to the query now on
// screen, and clearing the field drops the hits with it.
func TestHistorySearchDropsStaleAnswersAndClears(t *testing.T) {
	m, _, _ := historyModel(t)
	m.searching, m.search = true, "db-primary"
	m.rebuildRows()
	m.applyHistorySearch(historySearchMsg{query: "db-primary-07", hits: []search.Hit{{Key: "3", Count: 1, Score: 1}}})
	if m.historyHits != nil {
		t.Fatal("an answer to a query no longer on screen was applied")
	}
	m.applyHistorySearch(historySearchMsg{query: "db-primary", hits: []search.Hit{{Key: "3", Count: 1, Score: 1}}})
	if _, ok := m.historyHits["3"]; !ok {
		t.Fatal("the current query's answer was dropped")
	}
	if got := sessionNames(m); len(got) != 3 {
		t.Fatalf("rows after the answer: %v", got)
	}

	m.search = "db-primary-"
	m.rebuildRows()
	if got := sessionNames(m); len(got) != 2 {
		t.Fatalf("hits for an older query must not apply to a longer one: %v", got)
	}

	m.clearSearch()
	if m.historyHits != nil || m.historyQuery != "" {
		t.Fatal("clearing the field kept the hits")
	}
	if got := sessionNames(m); len(got) != 4 {
		t.Fatalf("rows after clear: %v", got)
	}
}

// The debounce only lets the newest keystroke's timer ask.
func TestHistorySearchDebounceIgnoresSupersededTimers(t *testing.T) {
	m, _, _ := historyModel(t)
	m.searching, m.search = true, "db-p"
	first := m.scheduleHistorySearch()
	m.search = "db-pr"
	second := m.scheduleHistorySearch()
	if first == nil || second == nil {
		t.Fatal("a query long enough for the index must arm the debounce")
	}
	if _, cmd := m.Update(historyDebounceMsg{seq: m.historySeq - 1}); cmd != nil {
		t.Fatal("a superseded timer asked the index")
	}
	if _, cmd := m.Update(historyDebounceMsg{seq: m.historySeq}); cmd == nil {
		t.Fatal("the newest timer did not ask")
	}

	// Two runes cannot be answered: no hits come back, and none linger.
	m.historyHits, m.historyQuery = map[string]search.Hit{"3": {Key: "3"}}, "db-pr"
	m.search = "db"
	if cmd := m.scheduleHistorySearch(); cmd == nil {
		t.Fatal("a short query still arms a timer; the index refuses it when asked")
	}
	m.applyHistorySearch(historySearchMsg{query: "db", hits: nil})
	if len(m.historyHits) != 0 {
		t.Fatalf("a refused query left hits: %v", m.historyHits)
	}
}

// The query rail is ranked: rows named for the query first, then rows
// showing it, then rows that said it, most and most recently first; the
// badge carries the count. Rows the query does not tell apart keep the
// board's order.
func TestQueryRailRanksByRelevance(t *testing.T) {
	m := &Model{width: 120, height: 30, collapsed: map[string]bool{}}
	m.sessions = []store.Session{
		{ID: "1", Name: "alpha", Tool: "claude", Status: status.Idle},
		{ID: "2", Name: "beta", Tool: "claude", Status: status.Idle},
		{ID: "3", Name: "gamma", Tool: "claude", Status: status.Idle},
		{ID: "4", Name: "delta-provider", Tool: "claude", Status: status.Idle},
		{ID: "5", Name: "epsilon", Tool: "claude", Status: status.Idle},
	}
	m.searchText = map[string]string{"3": "solving provider now"}
	m.history = search.New(search.Options{})
	m.searching, m.search = true, "provider"
	m.applyHistorySearch(historySearchMsg{query: "provider", hits: []search.Hit{
		{Key: "2", Count: 9, Score: 6.5},
		{Key: "1", Count: 1, Score: 0.4},
		{Key: "5", Count: 1, Score: 0.4},
	}})
	if got := sessionNames(m); fmt.Sprint(got) != "[delta-provider gamma beta alpha epsilon]" {
		t.Fatalf("ranked rail = %v", got)
	}
	rows := railTextAt(m, 70)
	if row := rows[lineWith(t, rows, "beta")]; !strings.Contains(row, "≡hist·9") {
		t.Fatalf("a many-hit row shows its count: %q", row)
	}
	if row := rows[lineWith(t, rows, "alpha")]; !strings.Contains(row, "≡hist") || strings.Contains(row, "·1") {
		t.Fatalf("a single hit shows no count: %q", row)
	}
}

// An index that changed re-asks the query on screen without a keystroke.
func TestHistoryIndexChangeReasksTheQueryOnScreen(t *testing.T) {
	m, index, transcript := historyModel(t)
	m.searching, m.search = true, "rotated the r2 token"
	m.rebuildRows()
	runHistoryQuery(t, m, m.search)
	if got := sessionNames(m); len(got) != 0 {
		t.Fatalf("nothing has said it yet: %v", got)
	}

	line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "text", "text": "I rotated the R2 token in the secret manager."},
	}}})
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(append(line, '\n'))
	f.Close()
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(transcript, later, later)
	p, err := index.Refresh([]search.Target{{Key: "3", Tool: search.ToolClaude, Path: transcript, AgentID: "quiet"}}, 1<<20)
	if err != nil || !p.Changed {
		t.Fatalf("append pass: %+v %v", p, err)
	}
	_, cmd := m.Update(historyIndexedMsg{progress: p})
	if cmd == nil {
		t.Fatal("an index change with a query on screen must re-ask")
	}
	m.applyCmd(t, cmd)
	if got := sessionNames(m); len(got) != 1 || got[0] != "quiet" {
		t.Fatalf("after the index caught up: %v", got)
	}
}
