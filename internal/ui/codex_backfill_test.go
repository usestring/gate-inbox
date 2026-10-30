package ui

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestCodexBackfillAfterAdoption(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "sessions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "rollout.jsonl")
	body := "A distinctive result from this particular session contains enough information to associate it with the right terminal pane safely."
	data := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"The initial task"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"final","content":[{"type":"output_text","text":"` + body + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE threads(id TEXT, cwd TEXT, rollout_path TEXT, archived INTEGER); INSERT INTO threads VALUES('01234567-89ab-cdef-0123-456789abcdef','/repo',?,0)`, path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(home, "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	launched := time.Now().UTC().Truncate(time.Second)
	if err := st.CreateSession(store.Session{ID: "row", Name: "row", Tool: "codex", Cwd: "/repo", TmuxSocket: "default", TmuxPaneID: "%1", AgentLaunchedAt: launched}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAgentLaunchedAt("row", launched); err != nil {
		t.Fatal(err)
	}
	panes := []codexBackfillPane{{id: "row", cwd: "/repo", socket: "default", pane: "%1", text: convo.Normalize(body), launchedAt: launched}}
	ix := &codexBackfillIndex{}
	candidates, err := ix.candidates(root, map[string]bool{"/repo": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d", len(candidates))
	}
	targets := bindCodexBackfills(st, panes, candidates, nil)
	target, ok := targets["row"]
	if !ok {
		t.Fatal("matching pane was not bound")
	}
	prompts, err := search.ReadFirstPrompts(target, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0] != "The initial task" {
		t.Fatalf("prompts = %q", prompts)
	}
	sess, err := st.Get("row")
	if err != nil {
		t.Fatal(err)
	}
	if sess.AgentSessionID != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("binding = %q", sess.AgentSessionID)
	}
}

func TestCodexBackfillRefusesAmbiguousOrClaimedConversations(t *testing.T) {
	text := strings.Repeat("distinctive ", 10)
	panes := []codexBackfillPane{{id: "a", cwd: "/repo", text: text}}
	candidate := codexBackfillCandidate{id: "one", cwd: "/repo", excerpts: []string{text}}
	if got := matchCodexBackfills(panes, []codexBackfillCandidate{candidate}, nil); len(got) != 1 {
		t.Fatal("unique match was rejected")
	}
	duplicate := candidate
	duplicate.id = "two"
	if got := matchCodexBackfills(panes, []codexBackfillCandidate{candidate, duplicate}, nil); len(got) != 0 {
		t.Fatal("ambiguous transcripts were matched")
	}
	panes = append(panes, codexBackfillPane{id: "b", cwd: "/repo", text: text})
	if got := matchCodexBackfills(panes, []codexBackfillCandidate{candidate}, nil); len(got) != 0 {
		t.Fatal("ambiguous panes were matched")
	}
	if got := matchCodexBackfills(panes[:1], []codexBackfillCandidate{candidate}, map[string]bool{"one": true}); len(got) != 0 {
		t.Fatal("claimed transcript was matched")
	}
}

func TestNamedAdoptedCodexRidesOpeningSweepBeforeBinding(t *testing.T) {
	m := buildModel(t)
	m.sessions = []store.Session{{ID: "row", Name: "chosen", Tool: "codex", NameSource: store.SourceUser, Cwd: "/repo", TmuxSocket: "default", TmuxPaneID: "%1"}}
	if m.autoNameScan() == nil {
		t.Fatal("unbound adopted Codex was excluded from the opening sweep")
	}
}
