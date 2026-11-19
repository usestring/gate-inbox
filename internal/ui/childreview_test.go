package ui

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/store"
)

// clippedReview is a pane a child showed on 2026-11-11, shortened
// and renamed: a review page taller than the pane, its tab row and
// heading scrolled off. The relay used to read its two numbered choices as a
// dialog for the user, and the parent asked its user "Enter or Escape?" on
// answers that user had already given.
const clippedReview = ` │   deploying I watch rollout, no_session and the
 │   fallback rate for 30 min.
   → Deploy search
 │ ● Merge router#1287 (fall back at once on
 │   no_session)? Blocked only on review.
   → Merge #1287

Ready to submit your answers?

❯ 1. Submit answers
  2. Cancel
`

func TestAChildOnItsReviewPageIsToldToSubmitNotAsked(t *testing.T) {
	sess := store.Session{ID: "c0ffee01", Name: "search-rollout-child", Tool: "claude"}
	body, key, urgent := childDialogBody(sess, clippedReview)
	for _, want := range []string{"review page", "Deploy search", "Merge #1287", "scrolled off",
		"do not ask them which key to press", `answer "Submit answers"`} {
		if !strings.Contains(body, want) {
			t.Errorf("review relay does not say %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"offering the keys", "your user's call", "AskUserQuestion"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("review relay still says %q:\n%s", unwanted, body)
		}
	}
	if !strings.HasPrefix(key, "r:") || urgent {
		t.Errorf("key %q urgent %v", key, urgent)
	}
}

// incompleteReview is the Submit tab of a two-question dialog with only the
// first question answered: the page warns, and the tab row still shows.
const incompleteReview = `──────────────────────────────────────────────────
←  ☒ Rollout  ☐ Approval  ✔ Submit  →

Review your answers

⚠ You have not answered all questions

 ● Which rollout path should I take?
   → Do both

Ready to submit your answers?

❯ 1. Submit answers
  2. Cancel
`

func TestAnIncompleteReviewPageRelaysTheMissingQuestion(t *testing.T) {
	const tool = "fake-review-cli"
	call := asks.Call{Tool: tool, ID: "call-1", Questions: []convo.AskQuestion{
		{Header: "Rollout", Question: "Which rollout path should I take?",
			Options: []convo.AskOption{{Label: "Do both"}, {Label: "Region only"}}},
		{Header: "Approval", Question: "Deploy the edge router now?",
			Options: []convo.AskOption{{Label: "Deploy", Description: "Fast-forward the deploy branch"}, {Label: "Hold"}}},
	}}
	asks.Register(tool, fakeAsks{call: call})
	dialog.RegisterTool(tool, dialog.ToolReader{})
	t.Cleanup(func() {
		asks.Register(tool, fakeAsks{})
		dialog.RegisterTool(tool, dialog.ToolReader{})
	})
	sess := store.Session{ID: "child001", Name: "rollout-child", Tool: tool, AgentSessionID: "conv"}
	body, key, _ := childDialogBody(sess, incompleteReview)
	for _, want := range []string{"Deploy the edge router now?", "Fast-forward the deploy branch", "headed Approval",
		"relay: true"} {
		if !strings.Contains(body, want) {
			t.Errorf("incomplete review relay does not say %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "do not ask them which key to press") || strings.HasPrefix(key, "r:") {
		t.Errorf("an incomplete review page was relayed as one to submit (key %q):\n%s", key, body)
	}
}
