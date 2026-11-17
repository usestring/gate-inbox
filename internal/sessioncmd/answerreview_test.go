package sessioncmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/relayask"
	"github.com/usestring/gate-inbox/internal/store"
)

// A child asking several long Approval questions at once ends on a review
// page taller than its pane. Measured live on Claude Code 2.1.321-322
// (2026-11-11/12): the tab row and "Review your answers" scroll off the top,
// so answer_session waited for a tab row that never came back ("question 3
// was keyed but its tab never ticked"), never pressed Submit, and every
// later call was refused -- until the parent asked its user which key to
// press on a page of answers that user had already given.

var longApprovals = []convo.AskQuestion{
	{Header: "Approval", Question: "Deploy search? Command: git push origin origin/main:refs/heads/deploy-search, a " +
		"fast-forward only. After deploying I watch rollout, no_session and the fallback rate for 30 min.",
		Options: []convo.AskOption{{Label: "Deploy search", Description: "Fast-forward deploy-search"}, {Label: "Hold", Description: "Leave it undeployed"}}},
	{Header: "Approval", Question: "Merge router#1287 (fall back at once on no_session; AI Mode re-asks once)? " +
		"Checks all pass; blocked only on review. Command: gh pr merge 1287 --squash --delete-branch --admin.",
		Options: []convo.AskOption{{Label: "Merge #1287", Description: "Admin squash merge"}, {Label: "Wait", Description: "Wait for a review"}}},
	{Header: "Approval", Question: "After #1287 merges, deploy the edge router? Command: git push origin " +
		"origin/main:refs/heads/deploy-edge, a fast-forward only. I re-check and stop if it holds anything else.",
		Options: []convo.AskOption{{Label: "Deploy edge", Description: "Fast-forward deploy-edge"}, {Label: "Hold", Description: "Leave it undeployed"}}},
}

var longApprovalAnswers = []QuestionAnswer{
	{Question: "1", Answer: "Deploy search"},
	{Question: "2", Answer: "Merge #1287"},
	{Question: "3", Answer: "Deploy edge"},
}

// newTallReview is the three-question dialog at 60 columns in a pane of 16
// rows: each question's page fits, and the review page does not.
func newTallReview() *tabbedDialog {
	sim := newTabbedDialog(longApprovals)
	sim.width, sim.height = 60, 16
	return sim
}

func TestTheTallReviewModelScrollsItsTabRowOff(t *testing.T) {
	sim := newTallReview()
	first, _ := sim.Capture()
	if _, ok := dialog.ParseStepper(first); !ok {
		t.Fatalf("a question page lost its tab row, so the model does not test what it should:\n%s", first)
	}
	copy(sim.answers, []string{"Deploy search", "Merge #1287", "Deploy edge"})
	sim.active = len(longApprovals)
	review, _ := sim.Capture()
	if _, ok := dialog.ParseStepper(review); ok || strings.Contains(review, "Review your answers") {
		t.Fatalf("the review page fits the pane, so the model does not test what it should:\n%s", ansi.Strip(review))
	}
}

func TestFillDialogSubmitsAReviewPageTallerThanThePane(t *testing.T) {
	fastSettle(t)
	sim := newTallReview()
	answered, err := fillDialog(sim, questionsOf(t, sim, longApprovals), longApprovalAnswers, true)
	if err != nil {
		t.Fatalf("fillDialog: %v (keys %v)", err, sim.keys)
	}
	if want := []string{"Deploy search", "Merge #1287", "Deploy edge"}; !reflect.DeepEqual(sim.answers, want) {
		t.Errorf("dialog holds %q, want %q", sim.answers, want)
	}
	if !sim.closed || !answered.Submitted || !answered.Verified || len(answered.Answers) != 3 {
		t.Errorf("closed = %v submitted = %v verified = %v answers = %+v",
			sim.closed, answered.Submitted, answered.Verified, answered.Answers)
	}
}

func TestFillDialogChecksATallReviewPageWithoutSubmitting(t *testing.T) {
	fastSettle(t)
	sim := newTallReview()
	answered, err := fillDialog(sim, questionsOf(t, sim, longApprovals), longApprovalAnswers, false)
	if err != nil {
		t.Fatalf("fillDialog: %v (keys %v)", err, sim.keys)
	}
	if sim.closed || answered.Submitted || !answered.Verified {
		t.Errorf("closed = %v submitted = %v verified = %v", sim.closed, answered.Submitted, answered.Verified)
	}
}

// The batch that stopped short leaves the dialog on the review page. Sending
// the same answers again submits it without keying any of them twice, and
// "Submit answers" alone does too.
func TestAnswersSentAgainOnATallReviewPageSubmitIt(t *testing.T) {
	for name, answers := range map[string][]QuestionAnswer{
		"the same answers": longApprovalAnswers,
		"Submit answers":   {{Answer: "Submit answers"}},
	} {
		t.Run(name, func(t *testing.T) {
			fastSettle(t)
			sim := newTallReview()
			copy(sim.answers, []string{"Deploy search", "Merge #1287", "Deploy edge"})
			sim.active = len(longApprovals)
			raw, _ := sim.Capture()
			review, ok := dialog.OnReview(raw)
			if !ok || !review.Clipped {
				t.Fatalf("review = %+v ok = %v", review, ok)
			}
			questions := questionsOf(t, sim, longApprovals)
			answered, err := answersOnReview(sim, questions, answers, review, true)
			if err != nil {
				t.Fatalf("answersOnReview: %v (keys %v)", err, sim.keys)
			}
			if !sim.closed || !answered.Submitted || !reflect.DeepEqual(sim.keys, []string{"Enter"}) {
				t.Errorf("closed = %v submitted = %v keys = %v", sim.closed, answered.Submitted, sim.keys)
			}
		})
	}
}

func TestAnAnswerTheReviewPageListsDifferentlyIsRefused(t *testing.T) {
	fastSettle(t)
	sim := newTallReview()
	copy(sim.answers, []string{"Deploy search", "Merge #1287", "Hold"})
	sim.active = len(longApprovals)
	raw, _ := sim.Capture()
	review, _ := dialog.OnReview(raw)
	_, err := answersOnReview(sim, questionsOf(t, sim, longApprovals), longApprovalAnswers, review, true)
	if err == nil || !strings.Contains(err.Error(), `answered "Hold", not "Deploy edge"`) || len(sim.keys) != 0 {
		t.Fatalf("err = %v keys = %v, want a refusal with nothing keyed", err, sim.keys)
	}
}

// clippedReviewPane is a pane read_session returned on 2026-11-11,
// shortened and renamed: the tab row, the
// heading and the first question's start are above the top of the screen.
const clippedReviewPane = ` │   deploying I watch rollout, no_session and the
 │   fallback rate for 30 min.
   → Deploy search
 │ ● Merge router#1287 (fall back at once
 │   on no_session; AI Mode re-asks once)? Checks pass;
 │   blocked only on review.
   → Merge #1287
 │ ● After #1287 merges, deploy the edge router?
 │   Command: git push origin
 │   origin/main:refs/heads/deploy-edge, a
 │   fast-forward only.
   → Deploy edge

Ready to submit your answers?

❯ 1. Submit answers
  2. Cancel
`

// submittingScript paints the review page and, on Enter, clears it and
// prints the record Claude Code prints of the answers it took, word-wrapped
// to the pane the way Claude Code wraps it.
const submittingScript = `cat "$1"
while IFS= read -rsn1 key; do
  if [[ -z $key ]]; then
    clear
    cat "$2"
    break
  fi
done
sleep 60
`

func childOnClippedReview(t *testing.T, h *sessionHarness) store.Session {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "submitting.sh")
	if err := os.WriteFile(script, []byte(submittingScript), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	width, _ := paneSize(clippedReviewPane)
	wrapper := &tabbedDialog{width: width - 1}
	record := "● User answered Claude's questions:\n"
	for i, answer := range []string{"Deploy search", "Merge #1287", "Deploy edge"} {
		lead := "     · "
		if i == 0 {
			lead = "  ⎿  · "
		}
		record += wrapper.wrap(lead, "     ", clippedReviewQuestions[i]+" → "+answer)
	}
	recordFile := filepath.Join(dir, "record.txt")
	if err := os.WriteFile(recordFile, []byte(record+"\n❯ \n"), 0o644); err != nil {
		t.Fatalf("write record: %v", err)
	}
	return childRunning(t, h, h.caller.ID, "child001", "search-rollout-child", clippedReviewPane,
		"bash "+script+" %s "+recordFile)
}

// clippedReviewQuestions are the questions clippedReviewPane reviews, whole.
var clippedReviewQuestions = []string{
	"Deploy search? After deploying I watch rollout, no_session and the fallback rate for 30 min.",
	"Merge router#1287 (fall back at once on no_session; AI Mode re-asks once)? Checks pass; blocked only on review.",
	"After #1287 merges, deploy the edge router? Command: git push origin origin/main:refs/heads/deploy-edge, a fast-forward only.",
}

func TestTheClippedReviewPaneReadsAsAReviewPage(t *testing.T) {
	review, ok := dialog.OnReview(clippedReviewPane)
	if !ok || !review.Clipped || review.Submit != 1 || review.Cursor != 1 {
		t.Fatalf("review = %+v ok = %v", review, ok)
	}
	answers := make([]string, len(review.Answers))
	for i, answer := range review.Answers {
		answers[i] = answer.Answer
	}
	if want := []string{"Deploy search", "Merge #1287", "Deploy edge"}; !reflect.DeepEqual(answers, want) {
		t.Errorf("answers = %q, want %q", answers, want)
	}
	if !dialog.SameQuestion(review.Answers[0].Question, longApprovals[0].Question) {
		t.Errorf("clipped first question %q does not read as the end of %q", review.Answers[0].Question, longApprovals[0].Question)
	}
}

// answer_session with every answer again, as the parent sent it after the
// timeout, relayed or not: the page is submitted, and a relay is not refused
// for want of a fresh answer from the user, because none is keyed.
func TestAnswerAllOnAClippedReviewPageSubmitsIt(t *testing.T) {
	for _, relay := range []bool{true, false} {
		name := "relay false"
		if relay {
			name = "relay true"
		}
		t.Run(name, func(t *testing.T) {
			h := newSessionHarness(t)
			child := childOnClippedReview(t, h)
			answered, err := h.sessions.AnswerAll(h.caller.ID, child.ID, []QuestionAnswer{
				{Answer: "Merge #1287", Question: clippedReviewQuestions[1]},
				{Answer: "Deploy edge", Question: clippedReviewQuestions[2]},
			}, true, relay)
			if err != nil {
				t.Fatalf("AnswerAll: %v", err)
			}
			if !answered.Submitted {
				t.Errorf("answered = %+v, want it submitted", answered)
			}
		})
	}
}

func TestSubmitAnswersOnAClippedReviewPageSubmitsIt(t *testing.T) {
	for _, relay := range []bool{true, false} {
		h := newSessionHarness(t)
		child := childOnClippedReview(t, h)
		answered, err := h.sessions.Answer(h.caller.ID, child.ID, "Submit answers", relay)
		if err != nil || !answered.Submitted {
			t.Fatalf("relay %v: Answer = %+v, %v", relay, answered, err)
		}
	}
}

// Keys on the review page are refused with the step to take instead, not a
// question for the user: the message parents obeyed by asking their user
// "Enter or Escape?" on answers that user had already given.
func TestKeysOnAClippedReviewPageSayToSubmitIt(t *testing.T) {
	h := newSessionHarness(t)
	child := childOnClippedReview(t, h)
	_, err := h.sessions.AnswerKeys(h.caller.ID, child.ID, []string{"Enter"}, true)
	if err == nil {
		t.Fatal("keys were sent")
	}
	for _, want := range []string{"do not ask them which key to press", `answer "Submit answers"`, "no keys were sent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "offering the keys") {
		t.Errorf("err = %v still tells the parent to ask its user for keys", err)
	}
}

func TestReadSessionOnAClippedReviewPageSaysToSubmitIt(t *testing.T) {
	h := newSessionHarness(t)
	child := childOnClippedReview(t, h)
	screen, err := h.sessions.Read(h.caller.ID, child.ID, "")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if screen.Digest.RelayAsk != nil {
		t.Errorf("relay_ask = %s, want none: there is nothing to ask the user", screen.Digest.RelayAsk.JSON())
	}
	if !strings.Contains(screen.Digest.Next, `answer "Submit answers"`) {
		t.Errorf("next = %q, want the Submit step", screen.Digest.Next)
	}
}

// A review page still missing an answer is not a page to submit: the missing
// question may be an Approval the user must be asked word for word, so it
// keeps its relay.
func TestAnIncompleteReviewPageKeepsTheRelayForTheMissingQuestion(t *testing.T) {
	asked := fourQuestions[:2]
	sim := newTabbedDialog(asked)
	sim.answers[0] = "Bun"
	sim.active = len(asked)
	raw, _ := sim.Capture()
	if _, ok := dialog.OnReview(raw); !ok {
		t.Fatalf("not read as a review page:\n%s", raw)
	}
	if next, done := (&Sessions{}).submitPageNext(store.Session{ID: "child001"}, raw); done {
		t.Fatalf("an incomplete review page reads as one to submit: %q", next)
	}
	ask := relayask.For(asked, dialog.Questions(raw, asked))
	if ask == nil || len(ask.Questions) != 1 || ask.Questions[0].Question != asked[1].Question {
		t.Fatalf("relay_ask = %+v, want the unanswered question 2 alone", ask)
	}
}
