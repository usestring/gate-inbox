package codexq

import (
	"os"
	"path/filepath"
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
