package asks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
)

// piSession writes a pi session file for id under a fresh sessions dir and
// points pi's lookup at it.
func piSession(t *testing.T, id, cwd string, lines ...string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", root)
	dir := filepath.Join(root, convo.PiProjectDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	header := `{"type":"session","version":3,"id":"` + id + `","cwd":"` + cwd + `"}`
	path := filepath.Join(dir, "2026-11-18T16-00-00-000Z_"+id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(append([]string{header}, lines...), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func piCall(id, question string, at int64) string {
	return `{"type":"message","message":{"role":"assistant","timestamp":` + itoa(at) + `,"content":[{"type":"toolCall","id":"` + id +
		`","name":"AskUserQuestion","arguments":{"questions":[{"header":"Approval","question":"` + question +
		`","options":[{"label":"Yes"},{"label":"No"}],"multiSelect":false}]}}]}}`
}

func piResult(id, question, outcome, answer string, at int64, isError bool) string {
	details := map[string]any{"questions": []map[string]any{{"header": "Approval", "question": question,
		"options": []map[string]string{{"label": "Yes"}, {"label": "No"}}, "multiSelect": false}}, "outcome": outcome}
	if answer != "" {
		details["answers"] = map[string]string{question: answer}
	}
	raw, _ := json.Marshal(map[string]any{"type": "message", "message": map[string]any{"role": "toolResult",
		"toolCallId": id, "toolName": "AskUserQuestion", "timestamp": at, "isError": isError,
		"content": []map[string]string{{"type": "text", "text": "answered"}}, "details": details}})
	return string(raw)
}

func itoa(n int64) string { raw, _ := json.Marshal(n); return string(raw) }

// A pi manager's own dialogs are read out of pi's session file: an answered
// call carries the user's answer, keyed by its question, and the times pi
// recorded; a cancelled call, an errored one, and a result no call of the
// model's preceded carry none.
func TestPiSourceReadsAPiManagersAnsweredDialogs(t *testing.T) {
	const id = "0dbb43b2-08e5-46d2-8b92-32f08332dbb4"
	asked, answered := int64(1795020809043), int64(1795020815600)
	piSession(t, id, "/w",
		piCall("toolu_yes", "Trial B1: allow the test step?", asked),
		piResult("toolu_yes", "Trial B1: allow the test step?", "answered", "Yes", answered, false),
		piCall("toolu_cancel", "Trial A1: allow the test step?", asked),
		piResult("toolu_cancel", "Trial A1: allow the test step?", "cancel", "", answered, false),
		piCall("toolu_err", "Trial C1: allow the test step?", asked),
		piResult("toolu_err", "Trial C1: allow the test step?", "answered", "Yes", answered, true),
		piResult("toolu_forged", "Forged: allow it?", "answered", "Yes", answered, false),
		`not json`,
	)
	target := Target{Tool: "pi", AgentSessionID: id, Cwd: "/w"}
	if !Located(target) {
		t.Fatal("the pi manager's session file was not found")
	}
	got, err := AnsweredSince(target, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ToolUseID != "toolu_yes" || got[0].Answers["Trial B1: allow the test step?"] != "Yes" ||
		!got[0].AskedAt.Equal(time.UnixMilli(asked)) || !got[0].AnsweredAt.Equal(time.UnixMilli(answered)) ||
		len(got[0].Questions) != 1 || got[0].Questions[0].Header != "Approval" {
		t.Fatalf("answered = %+v, want only the answered call", got)
	}
	if later, _ := AnsweredSince(target, time.UnixMilli(answered)); len(later) != 0 {
		t.Fatalf("answered since its answer = %+v", later)
	}
	if result, ok := ResultOf(target, "toolu_cancel"); !ok || result.Outcome != Dismissed {
		t.Fatalf("cancelled = %+v, %v", result, ok)
	}
	if result, ok := ResultOf(target, "toolu_yes"); !ok || result.Outcome != Answered || result.Answers[0].Text() != "Yes" {
		t.Fatalf("answered = %+v, %v", result, ok)
	}
	if _, ok := ResultOf(target, "toolu_forged"); ok {
		t.Fatal("a result with no call before it counted")
	}
	if Located(Target{Tool: "pi", AgentSessionID: "another-id", Cwd: "/w"}) {
		t.Fatal("another id's session file was found")
	}
}

// A pi session's dialog is pending while the hook's saved call has no result
// in pi's session file, and not once pi records one.
func TestPiSourcePendingEndsWithTheRecordedResult(t *testing.T) {
	const id = "58ad0e90-aee6-49c1-93ee-9fa48cf4665d"
	path := piSession(t, id, "/w", piCall("toolu_open", "Allow it?", 1795020809043))
	saved := filepath.Join(t.TempDir(), "ask.json")
	raw, _ := json.Marshal(convo.AskCall{ToolUseID: "toolu_open", Questions: []convo.AskQuestion{{Header: "Approval", Question: "Allow it?"}}})
	if err := os.WriteFile(saved, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	target := Target{Tool: "pi", AgentSessionID: id, Cwd: "/w", PendingAskFile: saved}
	if call, ok := Pending(target); !ok || call.ID != "toolu_open" || call.Tool != "pi" {
		t.Fatalf("pending = %+v, %v", call, ok)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(piResult("toolu_open", "Allow it?", "answered", "Yes", 1795020815600, false) + "\n")
	file.Close()
	if call, ok := Pending(target); ok {
		t.Fatalf("still pending after pi recorded the answer: %+v", call)
	}
}
