package search

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMessagesOnlyIncludeOperatorConversation(t *testing.T) {
	for _, tc := range []struct{ tool, data string }{
		{ToolCodex, `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Please fix it."}]}}
{"type":"event_msg","payload":{"type":"user_message","message":"Please fix it."}}
{"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"injected instructions"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /repo"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>machine</environment_context>"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"analysis","content":[{"type":"output_text","text":"private reasoning"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"commentary","recipient":"functions.exec","content":[{"type":"output_text","text":"tool instructions"}]}}
{"type":"response_item","payload":{"type":"function_call_output","output":"tool result"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"commentary","recipient":"all","content":[{"type":"output_text","text":"Checking now."}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"final","content":[{"type":"output_text","text":"Fixed."}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[`},
		{ToolClaude, `{"type":"user","message":{"role":"user","content":"Please fix it."}}
{"type":"user","isMeta":true,"message":{"role":"user","content":"injected instructions"}}
{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"summary"}}
{"type":"user","message":{"role":"user","content":[{"type":"text","synthetic":true,"text":"automatic continuation"},{"type":"text","ignored":true,"text":"ignored"}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"tool result"}]}}
{"type":"user","message":{"role":"user","content":"<task-notification>agent output</task-notification>"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private reasoning"},{"type":"text","text":"Checking now."},{"type":"tool_use","name":"Bash","input":{"command":"secret command"}}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Fixed."}]}}
`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := ReadMessages(Target{Tool: tc.tool, Path: path}, "")
			if err != nil {
				t.Fatal(err)
			}
			want := []Message{{"user", "Please fix it."}, {"assistant", "Checking now."}, {"assistant", "Fixed."}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		})
	}
}

func TestOpenCodeMessagesOmitToolsAndCombineTextParts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE session_v2 (id TEXT)`,
		`CREATE TABLE session_message (id TEXT, session_id TEXT, type TEXT, time_created INTEGER, data TEXT)`,
		`INSERT INTO session_message VALUES ('u','s','user',1,'{"text":"Please fix it."}'), ('a','s','assistant',2,'{"content":[{"type":"text","text":"Checking now."},{"type":"tool","text":"hidden"},{"type":"reasoning","text":"hidden"},{"type":"text","text":"Fixed."}]}'), ('idle','s','idle',3,'{"text":"hidden"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadMessages(Target{Tool: ToolOpenCode, AgentID: "s"}, path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Message{{"user", "Please fix it."}, {"assistant", "Checking now.\n\nFixed."}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}
