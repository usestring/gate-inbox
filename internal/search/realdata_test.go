package search

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Real-data checks for the two tools whose transcripts a fixture can only
// approximate: a Codex rollout on disk and opencode's live database. Both
// opt in with the same switch as the corpus benchmark, because both read
// private files:
//
//	GATE_SEARCH_BENCH_SESSIONS=1 go test ./internal/search -run 'TestReal' -v

func requireRealData(t *testing.T) string {
	t.Helper()
	if os.Getenv("GATE_SEARCH_BENCH_SESSIONS") == "" {
		t.Skip("set GATE_SEARCH_BENCH_SESSIONS to run against real transcripts")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	return home
}

func TestRealCodexRollout(t *testing.T) {
	root := filepath.Join(requireRealData(t), ".codex", "sessions")
	var biggest string
	var size int64
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".jsonl") && info.Size() > size {
			biggest, size = p, info.Size()
		}
		return nil
	})
	if biggest == "" {
		t.Skip("no Codex rollouts on this machine")
	}
	// rollout-<timestamp>-<uuid>.jsonl: the uuid is the resumable id.
	stem := strings.TrimSuffix(filepath.Base(biggest), ".jsonl")
	id := stem[len(stem)-36:]
	if tg, ok := NewLocator("", root).Target("row", ToolCodex, "", id); !ok || tg.Path != biggest {
		t.Fatalf("locator did not find %s by id %s: ok=%v %+v", biggest, id, ok, tg)
	}

	x := openTest(t, Options{})
	p, err := x.Refresh([]Target{{Key: "cx", Tool: ToolCodex, Path: biggest, AgentID: id}}, 64<<20)
	if err != nil || !p.Done || p.Rows == 0 {
		t.Fatalf("rollout indexed nothing: %+v %v", p, err)
	}
	// Whatever the first indexed turn says must be findable.
	data, _ := os.ReadFile(biggest)
	for _, line := range strings.Split(string(data), "\n") {
		text := LineText(ToolCodex, []byte(line), DefaultLimits)
		if text == "" {
			continue
		}
		word := firstLongWord(text)
		if got := hits(t, x, word); len(got) != 1 || got[0] != "cx" {
			t.Fatalf("query %q from the rollout's own turn: %v", word, got)
		}
		t.Logf("%s: %d turns, %q found", filepath.Base(biggest), p.Rows, word)
		return
	}
}

func TestRealOpenCodeParts(t *testing.T) {
	path := filepath.Join(requireRealData(t), ".local", "share", "opencode", "opencode.db")
	if _, err := os.Stat(path); err != nil {
		t.Skip("no opencode database on this machine")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sid, data string
	if err := db.QueryRow(`SELECT session_id, data FROM part WHERE data LIKE '{"type":"text"%' ORDER BY time_created DESC LIMIT 1`).Scan(&sid, &data); err != nil {
		t.Skip("no text parts in the opencode database")
	}
	var part struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(data), &part) != nil || firstLongWord(part.Text) == "" {
		t.Skip("newest text part has no searchable word")
	}

	x := openTest(t, Options{OpenCodeDB: path})
	p, err := x.Refresh([]Target{{Key: "oc", Tool: ToolOpenCode, AgentID: sid}}, 1<<20)
	if err != nil || !p.Done || p.Rows == 0 {
		t.Fatalf("opencode session indexed nothing: %+v %v", p, err)
	}
	word := firstLongWord(part.Text)
	if got := hits(t, x, word); len(got) != 1 || got[0] != "oc" {
		t.Fatalf("query %q from the session's newest text part: %v", word, got)
	}
	t.Logf("session %s: %d parts, %q found", sid, p.Rows, word)
}

// firstLongWord is a query the trigram index can answer, taken from text.
func firstLongWord(text string) string {
	for _, word := range strings.Fields(text) {
		word = strings.Trim(word, `"'<>()[]{}:;,.`)
		if len(word) >= 4 && !strings.ContainsAny(word, `*%_?[\`) {
			return word
		}
	}
	return ""
}
