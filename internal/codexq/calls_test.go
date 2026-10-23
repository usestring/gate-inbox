package codexq

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const questionsRollout = "testdata/rollout-0.157.0-questions.jsonl"

func TestReadCallsFollowsEachQuestionToItsOutcome(t *testing.T) {
	calls, err := ReadCalls(questionsRollout)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatalf("read %d calls, want 4", len(calls))
	}
	two, expired, async, other := calls[0], calls[1], calls[2], calls[3]
	if two.State != Answered || len(two.Questions) != 2 || two.Questions[0].ID != "color" ||
		len(two.Questions[0].Options) != 3 || two.Questions[0].Options[1].Description == "" {
		t.Fatalf("first call = %+v, want two answered questions with their options", two)
	}
	if got := two.Answers["size"]; len(got.Labels) != 1 || got.Labels[0] != "Large" || got.Note != "keep it subtle" {
		t.Errorf("size answer = %+v, want Large with its note", got)
	}
	if got := two.Answers["color"]; len(got.Labels) != 1 || got.Labels[0] != "Green" || got.Note != "" {
		t.Errorf("color answer = %+v, want Green", got)
	}
	if expired.State != Superseded || expired.ResolvedAt.IsZero() || expired.Answers != nil {
		t.Errorf("the timed-out call = %+v, want resolved with no answers and superseded by the next message", expired)
	}
	if !async.Async || async.State != Superseded || async.Questions[0].Question != "Which theme should the docs use?" ||
		len(async.Questions[0].Options) != 2 || async.Reply == "" {
		t.Errorf("the async call = %+v, want its title and options, answered by the next message", async)
	}
	if got := other.Answers["ci"]; len(got.Labels) != 1 || got.Labels[0] != NoneOfTheAbove || got.Note != "Buildkite, self-hosted" {
		t.Errorf("ci answer = %+v, want the note under None of the above", got)
	}
}

func TestCallsAtReadsOnlyWhatWasAppended(t *testing.T) {
	data, err := os.ReadFile(questionsRollout)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	cut := 0
	for i, n := 0, 0; i < len(data); i++ {
		if data[i] == '\n' {
			n++
			if n == 5 {
				cut = i + 1
				break
			}
		}
	}
	if err := os.WriteFile(path, data[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	calls, err := CallsAt(path)
	if err != nil || len(calls) != 1 || calls[0].State != Answered {
		t.Fatalf("first read = %+v, %v", calls, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	calls, err = CallsAt(path)
	if err != nil || len(calls) != 4 {
		t.Fatalf("second read = %d calls, %v; want 4", len(calls), err)
	}
}

func TestRolloutPathFindsTheConversation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "09", "30")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "rollout-2026-09-30T20-10-24-01a0f3f0-aaaa.jsonl")
	if err := os.WriteFile(want, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RolloutPath(root, "01a0f3f0-aaaa"); got != want {
		t.Fatalf("RolloutPath = %q, want %q", got, want)
	}
	if got := RolloutPath(root, "missing"); got != "" {
		t.Fatalf("RolloutPath of a missing conversation = %q", got)
	}
}

func TestRolloutPathFollowsResumes(t *testing.T) {
	root := t.TempDir()
	const id = "11111111-1111-4111-8111-111111111111"
	const resume = "22222222-2222-4222-8222-222222222222"
	write := func(name, sessionID string) string {
		t.Helper()
		path := filepath.Join(root, name)
		data, err := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"session_id": sessionID, "id": sessionID}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	old := write("rollout-2026-10-01T01-00-00-"+id+".jsonl", id)
	if got := RolloutPath(root, id); got != old {
		t.Fatalf("initial path = %q, want %q", got, old)
	}
	resumed := write("rollout-2026-10-01T02-00-00-"+id+"_"+resume+".jsonl", id)
	if got := RolloutPath(root, id); got != resumed {
		t.Fatalf("resumed path = %q, want %q", got, resumed)
	}
	write("rollout-2026-10-01T03-00-00-"+id+"_"+resume+".jsonl", resume)
	write("rollout-2026-10-01T04-00-00-prefix"+id+".jsonl", id)
	write("rollout-2026-10-01T05-00-00-"+resume+"_"+id+".jsonl", resume)
	if got := RolloutPath(root, id); got != resumed {
		t.Fatalf("unrelated rollout selected: %q, want %q", got, resumed)
	}
}

const asyncApprovalCall = `{"timestamp":"2026-10-01T01:00:00Z","type":"response_item","payload":{"type":"function_call","name":"request_user_input_async","call_id":"approval","arguments":"{\"questions\":[{\"title\":\"Run the proposed action?\",\"options\":[\"Go\",\"Defer\"]}]}"}}` + "\n"

func userReplyLine(t *testing.T, text string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"timestamp": "2026-10-01T01:01:00Z", "type": "response_item",
		"payload": map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": text}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func TestSyntheticPromptsLeaveQuestionsPending(t *testing.T) {
	for _, prompt := range []string{
		"<hook_prompt hook_run_id=\"stop:1\">Go</hook_prompt>",
		" \n<hook_prompt>Go</hook_prompt>",
		"[gate-inbox] From agent: Go",
	} {
		t.Run(prompt, func(t *testing.T) {
			data := asyncApprovalCall + userReplyLine(t, prompt)
			calls, err := readCalls(strings.NewReader(data))
			if err != nil || len(calls) != 1 || calls[0].State != Outstanding || calls[0].Reply != "" {
				t.Fatalf("synthetic input consumed question: %+v, %v", calls, err)
			}
			tracker := &Tracker{index: map[string]int{}}
			if _, err := tracker.consume(strings.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			if got := Unresolved(tracker.questions); len(got) != 1 {
				t.Fatalf("tracker lost pending ask: %+v", tracker.questions)
			}
			calls, err = readCalls(strings.NewReader(data + userReplyLine(t, "Go")))
			if err != nil || len(calls) != 1 || calls[0].State != Superseded || calls[0].Reply != "Go" {
				t.Fatalf("real reply after synthetic input = %+v, %v", calls, err)
			}
		})
	}
}

func TestAsyncReplyIsNotAssignedToAmbiguousQuestions(t *testing.T) {
	bundled := strings.Replace(asyncApprovalCall, `\"options\":[\"Go\",\"Defer\"]}`, `\"options\":[\"Go\",\"Defer\"]},{\"title\":\"Run another action?\",\"options\":[\"Go\",\"Defer\"]}`, 1)
	cases := map[string]string{
		"bundled":     bundled,
		"overlapping": asyncApprovalCall + strings.Replace(asyncApprovalCall, `"call_id":"approval"`, `"call_id":"other"`, 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			calls, err := readCalls(strings.NewReader(data + userReplyLine(t, "Go")))
			if err != nil {
				t.Fatal(err)
			}
			for _, call := range calls {
				if call.Reply != "" {
					t.Fatalf("ambiguous reply assigned: %+v", call)
				}
			}
		})
	}
}
