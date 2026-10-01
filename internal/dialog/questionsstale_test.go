package dialog

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/convo"
)

// claude-2.1.287-w120-approval-after-dead-ask.ansi is a real Claude Code
// 2.1.287 child at 120 columns: it was asked a three-option question about
// PR #190, its pane was killed with that dialog up, and the resumed
// conversation then asked a two-option Approval. The PR #190 call is what its
// ask-pending hook still holds.

var deadPR190Ask = []convo.AskQuestion{{
	Header:   "Decision",
	Question: "PR #190 is green; how should I proceed with it?",
	Options:  []convo.AskOption{{Label: "Merge it"}, {Label: "Wait for review"}, {Label: "Close it"}},
}}

var standingApproval = []convo.AskQuestion{{
	Header:   "Approval",
	Question: "May I run git push origin HEAD to push the merge?",
	Options:  []convo.AskOption{{Label: "Push merge", Description: "push it now"}, {Label: "Stop / defer", Description: "do nothing"}},
}}

// On 2026-10-01 read_session listed a parent the PR #190 question as on the
// screen of a child showing an Approval: a call that is not the dialog's is
// read off the screen instead.
func TestQuestionsIgnoreACallTheScreenIsNotShowing(t *testing.T) {
	pane := readScreenFixture(t, "claude-2.1.287-w120-approval-after-dead-ask.ansi")
	questions := Questions(pane, deadPR190Ask)
	if len(questions) != 1 {
		t.Fatalf("got %d questions, want the one on the screen: %+v", len(questions), questions)
	}
	got := questions[0]
	if !got.OnScreen || got.Question != "May I run git push origin HEAD to push the merge?" {
		t.Fatalf("on-screen question = %+v, want the Approval the pane shows", got)
	}
	var labels []string
	for _, option := range got.Options {
		labels = append(labels, option.Label)
	}
	if strings.Join(labels, "|") != "Push merge|Stop / defer" {
		t.Fatalf("options = %q, want the screen's", labels)
	}
	if rendered := RenderQuestions(questions); strings.Contains(rendered, "PR #190") {
		t.Fatalf("the stale call reached the rendering:\n%s", rendered)
	}
}

func TestQuestionsUseTheCallTheScreenIsShowing(t *testing.T) {
	questions := Questions(readScreenFixture(t, "claude-2.1.287-w120-approval-after-dead-ask.ansi"), standingApproval)
	if len(questions) != 1 || questions[0].Header != "Approval" || !questions[0].OnScreen {
		t.Fatalf("questions = %+v, want the call's Approval on the screen", questions)
	}
	if questions[0].Options[0].Description != "push it now" {
		t.Fatalf("options = %+v, want the call's descriptions", questions[0].Options)
	}
}

// A call asking as many questions as the dialog has tabs is still not this
// dialog's when the tab on the screen asks something else.
func TestQuestionsIgnoreAForeignCallOfTheSameLength(t *testing.T) {
	foreign := make([]convo.AskQuestion, 4)
	for i := range foreign {
		foreign[i] = convo.AskQuestion{Header: "Old", Question: "An earlier dialog's question?", Options: []convo.AskOption{{Label: "pnpm"}}}
	}
	questions := Questions(readScreenFixture(t, "claude-2.1.284-w50-stepper4-first.ansi"), foreign)
	if len(questions) != 4 {
		t.Fatalf("got %d questions, want the dialog's four", len(questions))
	}
	if questions[0].Header != "Tooling" || !questions[0].OnScreen ||
		questions[0].Question != "Which package manager should the workspace standardise on?" {
		t.Fatalf("on-screen question = %+v, want the Tooling tab as the screen draws it", questions[0])
	}
	for _, q := range questions {
		if q.Question == "An earlier dialog's question?" {
			t.Fatalf("the foreign call reached question %d: %+v", q.Index, q)
		}
	}
}
