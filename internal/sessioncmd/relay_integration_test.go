package sessioncmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/store"
)

// storefront is answerPane's question as the child's transcript records it.
var storefront = convo.AskQuestion{Header: "Approval", Question: "Which storefront should the census cover?",
	Options: []convo.AskOption{{Label: "Germany only"}, {Label: "Every storefront"}}}

// scriptedChild is a real pane running a scripted AskUserQuestion, filed as
// the harness caller's child, with transcripts for both on disk.
func scriptedChild(t *testing.T, h *sessionHarness, question convo.AskQuestion, parent *transcript, asked time.Time) store.Session {
	t.Helper()
	h.sessions.claudeHome = t.TempDir()
	child := childAnswering(t, h, h.caller.ID, "child001", "gitleaks-allowlist", answerPane,
		question.Question, "Germany only", "Every storefront")
	for id, conversation := range map[string]string{child.ID: "conv-child", h.caller.ID: "conv-parent"} {
		if err := h.store.SetAgentSessionID(id, conversation); err != nil {
			t.Fatal(err)
		}
	}
	(&transcript{}).ask(childCallID, asked, question).write(t, h.sessions.claudeHome, "conv-child")
	parent.write(t, h.sessions.claudeHome, "conv-parent")
	return child
}

func hookNote(t *testing.T, h *sessionHarness, child store.Session) string {
	t.Helper()
	return AskAnsweredHook(h.sessions.configDir, child.ID,
		[]byte(`{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_use_id":"`+childCallID+`"}`))
}

func TestRelayedApprovalReachesAScriptedChild(t *testing.T) {
	h := newSessionHarness(t)
	asked := time.Now().Add(-2 * time.Minute).UTC()
	parent := (&transcript{}).ask("toolu_parent", asked.Add(time.Minute), storefront).
		answer("toolu_parent", asked.Add(90*time.Second), map[string]string{storefront.Question: "Every storefront"})
	child := scriptedChild(t, h, storefront, parent, asked)

	_, err := h.sessions.Answer(h.caller.ID, child.ID, "Every storefront", false)
	if !errors.Is(err, errApprovalNeedsRelay) {
		t.Fatalf("unrelayed answer err = %v, want the Approval refusal", err)
	}
	if raw, _ := h.driver.CapturePane(child.ID); strings.Contains(ansi.Strip(raw), "User answered") {
		t.Fatalf("the refused answer reached the child:\n%s", raw)
	}

	_, err = h.sessions.Answer(h.caller.ID, child.ID, "Germany only", true)
	if !errors.Is(err, errRelayRefused) || !strings.Contains(err.Error(), `your user answered "Every storefront"`) {
		t.Fatalf("mismatched relay err = %v, want it refused naming the user's answer", err)
	}

	answered, err := h.sessions.Answer(h.caller.ID, child.ID, "Every storefront", true)
	if err != nil {
		t.Fatalf("relayed answer: %v", err)
	}
	if answered.Selected != "Every storefront" || !answered.Verified {
		t.Fatalf("answered %+v, want the second option read back", answered)
	}
	rows, err := h.store.AnswersFor(child.ID, childCallID)
	if err != nil || len(rows) != 1 || rows[0].Mode != store.AnswerRelayedUser || rows[0].State != store.AnswerKeyed ||
		rows[0].EvidenceToolUseID != "toolu_parent" {
		t.Fatalf("ledger = %+v, %v; want one keyed relayed_user row", rows, err)
	}
	if note := hookNote(t, h, child); !strings.Contains(note, "matched this answer word for word") {
		t.Fatalf("hook printed %q, want the relayed note", note)
	}
}

func TestAnAgentAnswerIsLedgeredAndNotedToTheChild(t *testing.T) {
	h := newSessionHarness(t)
	question := storefront
	question.Header = "Scope"
	child := scriptedChild(t, h, question, &transcript{}, time.Now().Add(-time.Minute).UTC())
	answered, err := h.sessions.Answer(h.caller.ID, child.ID, "Germany only", false)
	if err != nil || !answered.Verified {
		t.Fatalf("agent answer: %+v, %v", answered, err)
	}
	rows, err := h.store.AnswersFor(child.ID, childCallID)
	if err != nil || len(rows) != 1 || rows[0].Mode != store.AnswerByAgent || rows[0].BySession != h.caller.ID ||
		rows[0].State != store.AnswerKeyed {
		t.Fatalf("ledger = %+v, %v; want one keyed agent row", rows, err)
	}
	if note := hookNote(t, h, child); !strings.Contains(note, "It is not the user's approval.") ||
		!strings.Contains(note, h.caller.ID) {
		t.Fatalf("hook printed %q, want the agent note naming the parent", note)
	}
}
