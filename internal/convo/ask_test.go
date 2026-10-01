package convo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestPendingAskCallAndAnsweredAsks(t *testing.T) {
	q := AskQuestion{Header: "Approval", Question: "Deploy it?", Options: []AskOption{{Label: "Yes"}, {Label: "No"}}}
	qs, _ := json.Marshal([]AskQuestion{q})
	lines := []string{
		`{"type":"assistant","timestamp":"2026-09-28T19:50:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"AskUserQuestion","input":{"questions":` + string(qs) + `}}]}}`,
		`{"type":"user","timestamp":"2026-09-28T19:51:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"answered"}]},"toolUseResult":{"answers":{"Deploy it?":"Yes"}}}`,
		`{"type":"user","timestamp":"2026-09-28T19:51:30Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_orphan","content":"answered"}]},"toolUseResult":{"answers":{"Deploy it?":"Yes"}}}`,
		`{"type":"assistant","timestamp":"2026-09-28T19:52:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"AskUserQuestion","input":{"questions":` + string(qs) + `}}]}}`,
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call, ok := PendingAskCall(path)
	if !ok || call.ToolUseID != "toolu_2" || call.AskedAt.Minute() != 52 || call.Questions[0].Header != "Approval" {
		t.Fatalf("PendingAskCall = %+v, %v; want toolu_2 asked at :52", call, ok)
	}
	asks, err := AnsweredAsks(path, time.Time{})
	if err != nil || len(asks) != 1 || asks[0].ToolUseID != "toolu_1" || asks[0].Answers["Deploy it?"] != "Yes" ||
		asks[0].AnsweredAt.Minute() != 51 || asks[0].Questions[0].Question != "Deploy it?" {
		t.Fatalf("AnsweredAsks = %+v, %v; want toolu_1 alone, the orphan result skipped", asks, err)
	}
	if later, _ := AnsweredAsks(path, asks[0].AnsweredAt); len(later) != 0 {
		t.Fatalf("AnsweredAsks since its own answer = %+v, want none", later)
	}
}
