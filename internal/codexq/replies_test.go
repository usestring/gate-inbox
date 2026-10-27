package codexq

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func asyncAsk(id string, titles ...string) map[string]any {
	questions := []any{}
	for _, title := range titles {
		questions = append(questions, map[string]any{"title": title, "options": []string{"Approve", "Defer"}})
	}
	args, _ := json.Marshal(map[string]any{"questions": questions})
	return map[string]any{"type": "function_call", "name": askToolAsync, "call_id": id, "arguments": string(args)}
}

func replyEntry(tool, id string, index any, question, answer string) map[string]string {
	tuple, _ := json.Marshal([]any{tool, id, index})
	return map[string]string{"questionItemId": string(tuple), "question": question, "answer": answer}
}

func replyEnvelope(entries ...map[string]string) string {
	body, _ := json.Marshal(entries)
	return questionReplyOpen + "\n" + string(body) + "\n" + questionReplyClose
}

func replyRollout(t *testing.T, role string, messages ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	for _, payload := range []any{asyncAsk("other", "Approval for another action"), asyncAsk("chosen", "Approval for this action")} {
		if err := e.Encode(map[string]any{"timestamp": "2026-10-25T12:00:00Z", "payload": payload}); err != nil {
			t.Fatal(err)
		}
	}
	for _, message := range messages {
		if err := e.Encode(map[string]any{"timestamp": "2026-10-25T12:01:00Z", "payload": map[string]any{
			"type": "message", "role": role, "content": []any{map[string]string{"type": "input_text", "text": message}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func TestStructuredReplyBindsOnlyItsCall(t *testing.T) {
	data := replyRollout(t, "user", replyEnvelope(replyEntry(askToolAsync, "chosen", 0, "Approval for this", "Approve")), "What is the status?")
	calls, err := readCalls(bytes.NewReader(data))
	if err != nil || len(calls) != 2 || calls[0].State != Outstanding || calls[1].State != Answered {
		t.Fatalf("calls = %+v, %v", calls, err)
	}
	if got := calls[1].Answers[calls[1].Questions[0].ID]; len(got.Labels) != 1 || got.Labels[0] != "Approve" {
		t.Fatalf("bound answer = %+v", got)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var tracker Tracker
	questions, err := tracker.Update(path)
	if err != nil || len(Unresolved(questions)) != 1 || Unresolved(questions)[0].CallID != "other" {
		t.Fatalf("board pending = %+v, %v", questions, err)
	}
}

func TestStructuredReplyRejectsAmbiguousOrQuotedEvidence(t *testing.T) {
	valid := replyEntry(askToolAsync, "chosen", 0, "Approval for this", "Approve")
	for name, text := range map[string]string{
		"unknown call":        replyEnvelope(replyEntry(askToolAsync, "unknown", 0, "Approval for this", "Approve")),
		"wrong tool":          replyEnvelope(replyEntry(askTool, "chosen", 0, "Approval for this", "Approve")),
		"wrong index":         replyEnvelope(replyEntry(askToolAsync, "chosen", 1, "Approval for this", "Approve")),
		"null index":          replyEnvelope(replyEntry(askToolAsync, "chosen", nil, "Approval for this", "Approve")),
		"wrong question":      replyEnvelope(replyEntry(askToolAsync, "chosen", 0, "Different question", "Approve")),
		"duplicate target":    replyEnvelope(valid, valid),
		"mixed valid unknown": replyEnvelope(valid, replyEntry(askToolAsync, "unknown", 0, "Approval", "Approve")),
		"malformed":           questionReplyOpen + "{" + questionReplyClose,
		"agent":               "[gate-inbox] From agent\n" + replyEnvelope(valid),
		"quoted":              "```\n" + replyEnvelope(valid) + "\n```",
		"trailing text":       replyEnvelope(valid) + "\nquoted example",
	} {
		t.Run(name, func(t *testing.T) {
			calls, err := readCalls(bytes.NewReader(replyRollout(t, "user", text)))
			if err != nil || len(calls) != 2 || calls[0].State != Outstanding || calls[1].State != Outstanding {
				t.Fatalf("untrusted reply changed pending calls: %+v, %v", calls, err)
			}
		})
	}
	calls, _ := readCalls(bytes.NewReader(replyRollout(t, "assistant", replyEnvelope(valid))))
	if calls[1].State != Outstanding {
		t.Fatal("assistant reply became authority")
	}
}

func TestStructuredReplyCannotReplaceAnAnswer(t *testing.T) {
	calls, err := readCalls(bytes.NewReader(replyRollout(t, "user",
		replyEnvelope(replyEntry(askToolAsync, "chosen", 0, "Approval for this", "Defer")),
		replyEnvelope(replyEntry(askToolAsync, "chosen", 0, "Approval for this", "Approve")))))
	if err != nil || calls[1].Answers[calls[1].Questions[0].ID].Labels[0] != "Defer" {
		t.Fatalf("duplicate replaced answer: %+v, %v", calls, err)
	}
}
