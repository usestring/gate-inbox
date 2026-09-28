package convo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const askCallRecord = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_ask2","name":"AskUserQuestion","input":{"questions":[{"header":"Tooling","question":"Which package manager?","multiSelect":false,"options":[{"label":"Bun","description":"Fast"},{"label":"pnpm","description":"Strict"}]},{"header":"Checks","question":"Which checks must pass?","multiSelect":true,"options":[{"label":"Build","description":"Compile"}]}]}}]}}`

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPendingAskReadsTheCallStillWaiting(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_ask1","name":"AskUserQuestion","input":{"questions":[{"header":"Old","question":"Answered already?","options":[{"label":"Yes"}]}]}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_ask1","content":"Yes"}]}}`,
		askCallRecord,
	)
	questions, ok := PendingAsk(path)
	if !ok || len(questions) != 2 {
		t.Fatalf("ok = %v, questions = %+v", ok, questions)
	}
	if questions[0].Header != "Tooling" || questions[0].Options[1].Description != "Strict" || !questions[1].MultiSelect {
		t.Errorf("questions = %+v", questions)
	}
}

func TestPendingAskIsEmptyOnceAnswered(t *testing.T) {
	path := writeTranscript(t, askCallRecord,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_ask2","content":"answered"}]}}`)
	if questions, ok := PendingAsk(path); ok {
		t.Errorf("an answered call read as pending: %+v", questions)
	}
	if _, ok := PendingAsk(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Error("a missing transcript read as pending")
	}
}
