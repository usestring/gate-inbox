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

var shape = convo.AskQuestion{Header: "Shape", Question: "Which shape should the report take?",
	Options: []convo.AskOption{{Label: "Table"}, {Label: "Prose"}}}

func writeTranscript(t *testing.T, home, conversation string, records ...map[string]any) {
	t.Helper()
	var lines []string
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(raw))
	}
	path := filepath.Join(home, "projects", "any-project", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func askRecord(id string, at time.Time, questions ...convo.AskQuestion) map[string]any {
	return map[string]any{
		"type": "assistant", "timestamp": at.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{
			"type": "tool_use", "id": id, "name": "AskUserQuestion", "input": map[string]any{"questions": questions},
		}}},
	}
}

func answerRecord(id string, at time.Time, answers map[string]string) map[string]any {
	return map[string]any{
		"type": "user", "timestamp": at.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": id, "content": "answered",
		}}},
		"toolUseResult": map[string]any{"answers": answers},
	}
}

func TestClaudeCallsAreReadFromTheTranscript(t *testing.T) {
	home := t.TempDir()
	asked := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	target := Target{Tool: "claude", AgentSessionID: "conv", Cwd: "/nowhere", ClaudeHome: home}

	writeTranscript(t, home, "conv", askRecord("toolu_1", asked, shape))
	call, ok := Pending(target)
	if !ok || call.ID != "toolu_1" || call.Tool != "claude" || !call.AskedAt.Equal(asked) ||
		len(call.Questions) != 1 || call.Questions[0].Question != shape.Question {
		t.Fatalf("Pending = %+v, %v; want the call toolu_1", call, ok)
	}
	if result, ok := ResultOf(target, "toolu_1"); !ok || result.Outcome != Unresolved {
		t.Fatalf("ResultOf a pending call = %+v, %v; want it unresolved", result, ok)
	}

	writeTranscript(t, home, "conv", askRecord("toolu_1", asked, shape),
		answerRecord("toolu_1", asked.Add(time.Minute), map[string]string{shape.Question: "Prose"}))
	if _, ok := Pending(target); ok {
		t.Fatal("an answered call still reads as pending")
	}
	result, ok := Wait(target, "toolu_1", time.Second, 10*time.Millisecond)
	if !ok || result.Outcome != Answered || len(result.Answers) != 1 || result.Answers[0].Text() != "Prose" {
		t.Fatalf("ResultOf the answered call = %+v, %v; want Prose", result, ok)
	}
	answered, err := AnsweredSince(target, time.Time{})
	if err != nil || len(answered) != 1 || answered[0].Answers[shape.Question] != "Prose" {
		t.Fatalf("AnsweredSince = %+v, %v", answered, err)
	}
	if !Located(target) {
		t.Fatal("the transcript was not located")
	}
	if _, ok := Unanswered(target); ok {
		t.Fatal("Claude Code never resolves a question by itself, so nothing reads as lost")
	}
}

type fakeSource struct {
	call    Call
	results map[string]Result
}

func (f fakeSource) Traits() Traits                   { return Traits{Name: "Fake", Expires: time.Minute} }
func (f fakeSource) Located(Target) bool              { return true }
func (f fakeSource) Pending(Target) (Call, bool)      { return f.call, f.call.ID != "" }
func (f fakeSource) Unanswered(Target) (Result, bool) { return Result{}, false }
func (f fakeSource) Result(_ Target, id string) (Result, bool) {
	result, ok := f.results[id]
	return result, ok
}
func (f fakeSource) Answered(Target, time.Time) ([]convo.AnsweredAsk, error) { return nil, nil }

func TestARegisteredSourceAnswersForItsTool(t *testing.T) {
	Register("fake-tool", fakeSource{call: Call{Tool: "fake-tool", ID: "c1", Questions: []convo.AskQuestion{shape}},
		results: map[string]Result{"c1": {Outcome: Expired}}})
	t.Cleanup(func() {
		mu.Lock()
		delete(sources, "fake-tool")
		mu.Unlock()
	})
	target := Target{Tool: "fake-tool", AgentSessionID: "x"}
	if got := PendingQuestions(target); len(got) != 1 || got[0].Header != "Shape" {
		t.Fatalf("PendingQuestions = %+v", got)
	}
	if result, ok := Wait(target, "c1", time.Second, time.Millisecond); !ok || result.Outcome != Expired {
		t.Fatalf("Wait = %+v, %v; want expired", result, ok)
	}
	if TraitsOf("fake-tool").Expires != time.Minute || TraitsOf("no-such-tool").Name != "Claude Code" {
		t.Fatal("traits are not read from the registered source")
	}
	if _, ok := Pending(Target{Tool: "fake-tool"}); ok {
		t.Fatal("a session with no conversation id has no record to read")
	}
}

func TestRegisteredTextJoinsLabelsAndNote(t *testing.T) {
	for _, tc := range []struct {
		in   Registered
		want string
	}{
		{Registered{Labels: []string{"Lint", "E2E tests"}}, "Lint, E2E tests"},
		{Registered{Labels: []string{"None of the above"}, Note: "SQLite"}, "None of the above (SQLite)"},
		{Registered{Note: "just words"}, "just words"},
	} {
		if got := tc.in.Text(); got != tc.want {
			t.Errorf("%+v.Text() = %q, want %q", tc.in, got, tc.want)
		}
	}
}
