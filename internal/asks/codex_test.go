package asks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codexHome(t *testing.T, rollout string) Target {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "09", "30")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "codexq", "testdata", rollout))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-09-30T20-10-24-conv-1.jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	return Target{Tool: "codex", AgentSessionID: "conv-1"}
}

func TestCodexRolloutAnswersForItsSession(t *testing.T) {
	target := codexHome(t, "rollout-0.157.0-questions.jsonl")
	if !Located(target) {
		t.Fatal("the rollout was not found")
	}
	if _, ok := Pending(target); ok {
		t.Fatal("a rollout whose every question is resolved reads as pending")
	}
	result, ok := ResultOf(target, "call_a7AXqTVQON0dFQetCSSoXFlS")
	if !ok || result.Outcome != Answered || len(result.Answers) != 2 || result.Answers[0].Text() != "Green" ||
		result.Answers[1].Text() != "Large (keep it subtle)" {
		t.Fatalf("ResultOf the answered call = %+v, %v", result, ok)
	}
	if result, ok := ResultOf(target, "call_GE4Y8tNHQZxNScgaaU3rw4AO"); !ok || result.Outcome != Expired {
		t.Fatalf("ResultOf the timed-out call = %+v, %v; want expired", result, ok)
	}
	async, ok := ResultOf(target, "call_6FSoiGvmBX8Wg2ZrLJJE88Xx")
	if !ok || async.Outcome != Answered || !strings.Contains(async.Reply, "Run exactly this shell command") {
		t.Fatalf("ResultOf the async call = %+v, %v; want it answered by the next message", async, ok)
	}
	answered, err := AnsweredSince(target, async.Call.AskedAt)
	if err != nil || len(answered) != 1 || answered[0].Answers["Which CI?"] != "Buildkite, self-hosted" {
		t.Fatalf("AnsweredSince = %+v, %v; want the note as the answer", answered, err)
	}
	if TraitsOf("codex").Expires != CodexExpiry || TraitsOf("codex").MultiSelectAnswerable {
		t.Fatal("codex traits are wrong")
	}
}

func TestACodexQuestionLeftToExpireIsLost(t *testing.T) {
	target := codexHome(t, "expired-blocking.jsonl")
	lost, ok := Unanswered(target)
	if !ok || lost.Outcome != Expired || len(lost.Call.Questions) == 0 {
		t.Fatalf("Unanswered = %+v, %v; want the expired question", lost, ok)
	}
}
