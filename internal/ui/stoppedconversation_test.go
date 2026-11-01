package ui

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func stoppedConversationModel(tool, id string, locator *search.Locator) *Model {
	return &Model{
		mode:         modeList,
		cfg:          config.Config{Tools: map[string]config.Tool{tool: {SessionStore: tool}}},
		rows:         []treeRow{{sess: store.Session{ID: "stopped", Tool: tool, Status: status.Dead, AgentSessionID: id}}},
		conversation: &conversationView{locator: locator, compact: true, hovered: -1},
	}
}

func TestStoppedConversationReadsExactEngineAndIDWithoutPane(t *testing.T) {
	for _, tool := range []string{"claude", "codex", "opencode"} {
		t.Run(tool, func(t *testing.T) {
			root := t.TempDir()
			id := "saved-conversation"
			var path, data string
			switch tool {
			case "claude":
				path = filepath.Join(root, "projects", "demo", id+".jsonl")
				data = `{"type":"user","message":{"role":"user","content":"Repair the parser."}}
{"type":"assistant","message":{"role":"assistant","content":"The parser is repaired."}}
`
			case "codex":
				path = filepath.Join(root, "rollout-"+id+".jsonl")
				data = `{"type":"response_item","payload":{"type":"message","role":"user","content":"Repair the parser."}}
{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"final","content":"The parser is repaired."}}
`
			case "opencode":
				database := filepath.Join(root, "history.db")
				t.Setenv("GATE_OPENCODE_DB", database)
				db, err := sql.Open("sqlite", database)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				for _, statement := range []string{
					`CREATE TABLE session_v2 (id TEXT)`,
					`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, time_created INTEGER, data TEXT)`,
					`INSERT INTO session_message VALUES ('u','saved-conversation','user',1,'{"text":"Repair the parser."}'), ('a','saved-conversation','assistant',2,'{"content":[{"type":"text","text":"The parser is repaired."}]}'), ('other','another-conversation','user',3,'{"text":"Unrelated work"}')`,
				} {
					if _, err := db.Exec(statement); err != nil {
						t.Fatal(err)
					}
				}
			}
			if path != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			m := stoppedConversationModel(tool, id, search.NewLocator(root, root))
			delete(m.cfg.Tools, tool)
			m.applyConversation(m.readConversation()().(conversationMsg))
			if m.conversation.err != nil {
				t.Fatal(m.conversation.err)
			}
			got := ansi.Strip(strings.Join(m.conversationRows(80, 30), "\n"))
			for _, want := range []string{"Repair the parser.", "The parser is repaired."} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q: %s", want, got)
				}
			}
			if strings.Contains(got, "Unrelated work") {
				t.Fatal("read another conversation")
			}
			if m.tmux != nil {
				t.Fatal("reading saved history required a pane")
			}
			header := ansi.Strip(strings.Join(m.stoppedConversationHeader(80), "\n"))
			if !strings.Contains(header, "Stopped") || !strings.Contains(header, tool) || !strings.Contains(header, id) {
				t.Fatalf("missing stopped identity: %s", header)
			}
			if tool != "opencode" && !strings.Contains(header, "Started with: Repair the parser.") {
				t.Fatalf("missing original request: %s", header)
			}
			m.applyConversation(m.readConversation()().(conversationMsg))
			if got := ansi.Strip(strings.Join(m.stoppedConversationHeader(80), "\n")); got != header {
				t.Fatalf("unchanged transcript changed the summary: %s", got)
			}
		})
	}
}

func TestStoppedConversationDoesNotShowPreviousSessionWhileLoading(t *testing.T) {
	m := stoppedConversationModel("codex", "missing", search.NewLocator("", t.TempDir()))
	m.conversation.key = conversationKey("other", "other-conversation")
	m.conversation.messages = []search.Message{{Role: "assistant", Text: "Another session's answer"}}
	m.conversation.dirty, m.conversation.busy = true, true
	got := ansi.Strip(strings.Join(m.conversationRows(80, 20), "\n"))
	if strings.Contains(got, "Another session") || !strings.Contains(got, "Loading saved conversation") {
		t.Fatalf("misleading preview while loading: %s", got)
	}
	m.conversation.busy = false
	m.applyConversation(m.readConversation()().(conversationMsg))
	got = ansi.Strip(strings.Join(m.conversationRows(120, 20), "\n"))
	if !strings.Contains(got, "no saved codex conversation") || strings.Contains(got, "Another session") {
		t.Fatalf("missing history was not explained: %s", got)
	}
	m.rows[0].sess.AgentSessionID = ""
	m.applyConversation(m.readConversation()().(conversationMsg))
	got = ansi.Strip(strings.Join(m.conversationRows(120, 20), "\n"))
	if !strings.Contains(got, "no conversation ID was recorded") {
		t.Fatalf("missing ID was not explained: %s", got)
	}
}

func TestStoppedConversationReloadsOpeningAfterLiveRead(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "rollout-saved.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"response_item","payload":{"type":"message","role":"user","content":"Original task"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := stoppedConversationModel("codex", "saved", search.NewLocator("", root))
	m.rows[0].sess.Status = status.Idle
	m.applyConversation(m.readConversation()().(conversationMsg))
	m.rows[0].sess.Status = status.Dead
	m.applyConversation(m.readConversation()().(conversationMsg))
	if m.conversation.opening != "Original task" {
		t.Fatalf("stopping did not load the original task: %q", m.conversation.opening)
	}
}
