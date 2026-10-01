package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/store"
)

func codexChild(t *testing.T, fixture string, keep int) store.Session {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "codexq", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if keep > 0 {
		lines := bytes.SplitAfter(data, []byte("\n"))
		data = bytes.Join(lines[:keep], nil)
	}
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "09", "30")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := strings.ReplaceAll(t.Name(), "/", "-")
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-09-30T20-10-24-"+id+".jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	return store.Session{ID: "child001", Name: "banner-child", Tool: "codex", AgentSessionID: id}
}

func codexPane(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", "codex", "codex-0.157.0-"+name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestACodexQuestionIsRelayedWholeAndAtOnce(t *testing.T) {
	for _, width := range []string{"40", "50", "60", "100"} {
		t.Run(width, func(t *testing.T) {
			sess := codexChild(t, "rollout-0.157.0-questions.jsonl", 3)
			body, key, urgent := childDialogBody(sess, codexPane(t, "ask2-q1-w"+width+".txt"))
			for _, want := range []string{
				"a dialog asking 2 questions",
				"Question 1 of 2 [Color]", "Which color should the banner use?", "1. Red -- Use a red banner.",
				"Question 2 of 2 [Size]", "How big should the banner be?", "2. Large -- Use a large banner.",
				"Codex resolves this question by itself", "at about 20:12:39 UTC",
				`put in the note of its "None of the above" row instead`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("relay does not say %q:\n%s", want, body)
				}
			}
			if strings.Contains(body, "None of the above --") {
				t.Errorf("Codex's own None of the above row was relayed as a choice:\n%s", body)
			}
			if !strings.HasPrefix(key, "q:") || !urgent {
				t.Errorf("key %q urgent %v; want it sent as an interrupt", key, urgent)
			}
		})
	}
}

func TestAnExpiredCodexQuestionIsRelayedAsExpired(t *testing.T) {
	sess := codexChild(t, "expired-blocking.jsonl", 0)
	body, key, urgent := childDialogBody(sess, "› Ask Codex to do anything\n")
	for _, want := range []string{"expired with no answer: Codex resolved it by itself", "Which colour should the probe use?",
		"answer_session can no longer reach it", "send_session"} {
		if !strings.Contains(body, want) {
			t.Errorf("relay does not say %q:\n%s", want, body)
		}
	}
	if !strings.HasPrefix(key, "x:") || !urgent {
		t.Errorf("key %q urgent %v", key, urgent)
	}
}

func TestACodexAsyncQuestionIsRelayedWithHowToAnswerIt(t *testing.T) {
	sess := codexChild(t, "rollout-0.157.0-questions.jsonl", 9)
	if call, ok := asks.Pending(childAskTarget(sess)); !ok || !call.Async {
		t.Fatalf("fixture cut does not leave the async call pending: %+v", call)
	}
	body, key, urgent := childDialogBody(sess, "• Which theme should the docs use?\n  • Light\n  • Dark\n\n› Ask Codex to do anything\n")
	for _, want := range []string{"asked a question without a dialog", "Which theme should the docs use?", "1. Light", "2. Dark",
		"answer_session on session child001, which sends your answer as that message"} {
		if !strings.Contains(body, want) {
			t.Errorf("relay does not say %q:\n%s", want, body)
		}
	}
	if !strings.HasPrefix(key, "a:") || urgent {
		t.Errorf("key %q urgent %v", key, urgent)
	}
}

func TestACodexApprovalIsRelayedAsAPersonsCall(t *testing.T) {
	for _, capture := range []string{"exec-approval-w40.txt", "patch-approval-w60.txt", "mcp-tool-approval-w50.txt"} {
		t.Run(capture, func(t *testing.T) {
			sess := codexChild(t, "rollout-0.157.0-questions.jsonl", 2)
			body, key, _ := childDialogBody(sess, codexPane(t, capture))
			if !strings.HasPrefix(key, "s:") || !strings.Contains(body, "has stopped on a permission prompt") ||
				!strings.Contains(body, "relay: true") || !strings.Contains(body, "1. ") {
				t.Fatalf("relay:\n%s", body)
			}
		})
	}
}

func TestThePreviewCardShowsACodexDialog(t *testing.T) {
	sess := codexChild(t, "rollout-0.157.0-questions.jsonl", 3)
	m := drainFleet(t)
	m.rows[m.cursor].sess.Tool = "codex"
	m.rows[m.cursor].sess.AgentSessionID = sess.AgentSessionID
	selected, _ := m.selected()
	m.preview = codexPane(t, "ask2-q2-w100.txt")
	m.askQuestions = map[string][]convo.AskQuestion{selected.ID: asks.PendingQuestions(childAskTarget(sess))}
	card := strippedRows(m.previewQuestions(50, 40))
	for _, want := range []string{"2 questions", "□ 1. Color", "□ 2. Size  ◂ on screen", "How big should the banner be?", "2. Large"} {
		if !strings.Contains(card, want) {
			t.Errorf("card does not show %q:\n%s", want, card)
		}
	}
	m.preview = codexPane(t, "exec-approval-w50.txt")
	m.askQuestions = nil
	card = strippedRows(m.previewQuestions(50, 40))
	if !strings.Contains(card, "permission prompt · a person's to answer") || !strings.Contains(card, "curl -sS") {
		t.Errorf("approval card:\n%s", card)
	}
}
