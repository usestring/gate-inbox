package sessioncmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/store"
)

func approvalOf(question string) convo.AskQuestion {
	return convo.AskQuestion{Header: "Approval", Question: question,
		Options: []convo.AskOption{{Label: ApproveOption}, {Label: "Don't"}}}
}

// The approval spent is the session's own, for exactly the question asked,
// recent, the user's, and spent once.
func TestSpendApprovalTakesOnlyTheUsersOwnFreshExactYesOnce(t *testing.T) {
	h, _ := grantHarness(t)
	const action = "Run `gate-inbox charter set manager.model=x --session abc` from session p?"
	now := time.Now()
	write := func(tr *transcript) { tr.write(t, h.sessions.claudeHome, "conv-parent") }

	write(&transcript{})
	if err := h.sessions.SpendApproval(h.caller.ID, action, now); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("no dialog: err = %v", err)
	}

	asked := now.Add(-time.Minute)
	write((&transcript{}).ask("toolu_no", asked, approvalOf(action)).
		answer("toolu_no", asked.Add(time.Second), map[string]string{action: "Don't"}))
	if err := h.sessions.SpendApproval(h.caller.ID, action, now); !errors.Is(err, ErrNotApproved) ||
		!strings.Contains(err.Error(), `answered "Don't"`) {
		t.Fatalf("refusal: err = %v", err)
	}

	write((&transcript{}).ask("toolu_other", asked, approvalOf("Run something else?")).
		answer("toolu_other", asked.Add(time.Second), map[string]string{"Run something else?": ApproveOption}))
	if err := h.sessions.SpendApproval(h.caller.ID, action, now); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("another question's yes: err = %v", err)
	}

	old := now.Add(-time.Hour)
	write((&transcript{}).ask("toolu_old", old, approvalOf(action)).
		answer("toolu_old", old.Add(time.Second), map[string]string{action: ApproveOption}))
	if err := h.sessions.SpendApproval(h.caller.ID, action, now); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("hour-old yes: err = %v", err)
	}

	write((&transcript{}).ask("toolu_agent", asked, approvalOf(action)).
		answer("toolu_agent", asked.Add(time.Second), map[string]string{action: ApproveOption}))
	if _, err := h.store.RecordAnswer(store.DialogAnswer{TargetSession: h.caller.ID, TargetToolUseID: "toolu_agent",
		QuestionHash: "x", Answer: ApproveOption, BySession: "someone", Mode: store.AnswerByAgent}); err != nil {
		t.Fatal(err)
	}
	if err := h.sessions.SpendApproval(h.caller.ID, action, now); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("agent-keyed yes: err = %v", err)
	}

	write((&transcript{}).ask("toolu_yes", asked, approvalOf(action)).
		answer("toolu_yes", asked.Add(time.Second), map[string]string{action: ApproveOption}))
	if err := h.sessions.SpendApproval(h.caller.ID, action, now); err != nil {
		t.Fatalf("the user's yes: %v", err)
	}
	if err := h.sessions.SpendApproval(h.caller.ID, action, now); !errors.Is(err, ErrNotApproved) ||
		!strings.Contains(err.Error(), "already been spent") {
		t.Fatalf("second spend: err = %v", err)
	}
}
