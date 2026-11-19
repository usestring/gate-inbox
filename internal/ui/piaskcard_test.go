package ui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
)

// A pi row waiting on AskUserQuestion shows the same card a Claude row does:
// the question, its options and how many are answered. The call comes from
// the file pi's ask-pending hook saved, since pi has no transcript to read
// it from, and the dialog is read off a real pi 1.5.1 capture.
func TestAPiQuestionDrawsTheQuestionCard(t *testing.T) {
	m := questionPreview(t, "pi-1.5.1-w80-ask-approval.ansi", nil)
	m.rows[m.cursor].sess.Tool = "pi"
	sess, _ := m.selected()

	manager := hooks.NewManager(t.TempDir())
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	call := convo.AskCall{ToolUseID: "pi-ask-1", AskedAt: time.Now(), Questions: []convo.AskQuestion{{
		Header:   "Approval",
		Question: "May I delete the directory /tmp/sample-9000/scratchxxxxxx/w/cache?",
		Options:  []convo.AskOption{{Label: "Approve", Description: "Delete it"}, {Label: "Deny", Description: "Keep it"}},
	}}}
	raw, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.PendingAskFile(sess.ID), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &poller{hooks: manager}
	asked := p.pendingAsk(sess, ansi.Strip(m.preview), map[string]*askRead{})
	if len(asked) != 1 || asked[0].Header != "Approval" {
		t.Fatalf("the poll read %+v from pi's saved call", asked)
	}
	m.askQuestions[sess.ID] = asked

	text := strippedRows(m.conversationRows(80, 40))
	for _, want := range []string{"1 question · 0 answered", "Approval", "May I delete the directory", "1. Approve", "Delete it", "2. Deny", "Keep it"} {
		if !strings.Contains(text, want) {
			t.Errorf("the card does not show %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Chat about this") {
		t.Errorf("the card shows the dialog's tail rather than its question:\n%s", text)
	}
}
