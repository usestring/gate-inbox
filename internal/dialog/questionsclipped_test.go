package dialog

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/convo"
)

// On 2026-10-17 a child's Approval was taller than its pane. Claude Code drew
// a window of the question: its start scrolled off the top and its end cut
// with "…". The call was read off the transcript in full, but the window
// matched it as neither a prefix nor a suffix, so the call was dropped and
// the window went to the parent with the header "Question". The parent was
// told to relay it word for word and could not.

var clippedApproval = []convo.AskQuestion{{
	Header: "Approval",
	Question: "Approve moving the queue workers from the old cluster to the new one?\n\n" +
		"Rollback if the new cluster is unhealthy: kubectl --context new -n jobs scale " +
		"deploy/queue-worker --replicas=1.\n\nKnown costs:\n" +
		"- Jobs still in progress on the old cluster at cutover lose their state, so the number is unmeasured.\n" +
		"- Consumers are down for about a minute; queued messages wait, nothing is dropped.",
	Options: []convo.AskOption{
		{Label: "Approve; human review merge"},
		{Label: "Approve; parent --admin merge"},
		{Label: "Stop / defer"},
	},
}}

const clippedApprovalPane = "──────────────────────────────────────────────────\n" +
	" ☐ Approval \n" +
	"\n" +
	"deploy/queue-worker --replicas=1.\n" +
	"\n" +
	"Known costs:\n" +
	"- Jobs still in progress on the old cluster\n" +
	"at cutover lose their state, so the number is\n" +
	"unmeasured.\n" +
	"- …\n" +
	"\n" +
	"❯ 1. Approve; human review merge\n" +
	"  2. Approve; parent --admin merge\n" +
	"  3. Stop / defer\n" +
	"  4. Type something.\n" +
	"──────────────────────────────────────────────────\n" +
	"  5. Chat about this\n" +
	"\n" +
	"Enter to select · ↑/↓ to navigate · Esc to cancel\n"

func TestQuestionsUseTheCallBehindAPromptClippedAtBothEnds(t *testing.T) {
	questions := Questions(clippedApprovalPane, clippedApproval)
	if len(questions) != 1 {
		t.Fatalf("got %d questions: %+v", len(questions), questions)
	}
	got := questions[0]
	if got.Header != "Approval" || got.Question != clippedApproval[0].Question || !got.OnScreen {
		t.Fatalf("question = %+v, want the call's Approval in full", got)
	}
	if strings.Contains(RenderQuestions(questions), "…") {
		t.Fatalf("the clipped window reached the rendering:\n%s", RenderQuestions(questions))
	}
}

func TestSameQuestionReadsAWindowCutWithAnEllipsis(t *testing.T) {
	full := "Which rollout path should I take for the router, and when should it start?"
	if !SameQuestion("path should I take for the router, and …", full) ||
		!SameQuestion("Which rollout path should I …", full) {
		t.Fatal("a window of the question should read as it")
	}
	// Without the ellipsis the screen drew the question's whole end, so a
	// middle slice is some other question.
	if SameQuestion("path should I take for the router", full) ||
		SameQuestion("path should I take for the gateway, and …", full) ||
		SameQuestion("…", full) {
		t.Fatal("SameQuestion matched a question it is not")
	}
}
