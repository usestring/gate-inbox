package sessioncmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
)

// rollout is the question the wrong answers of 2026-09-28 were given to, and
// plan the numbered list its child wrote above the dialog.
var (
	rollout = convo.AskQuestion{Header: "Rollout", Question: "Which rollout path should I take?",
		Options: []convo.AskOption{
			{Label: "Hold for staged UI", Description: "Wait until the staged UI rework ships"},
			{Label: "Only region #4021", Description: "Deploy only the region change"},
			{Label: "Do both", Description: "Roll out both in parallel"},
		}}
	notify = convo.AskQuestion{Header: "Notify", Question: "Should I notify the channel when it lands?",
		Options: []convo.AskOption{{Label: "Yes, post in the channel", Description: "Post once"}, {Label: "No", Description: "Stay quiet"}}}
	plan = []string{
		"1. Hold for staged UI until the rework ships",
		"2. Only region #4021, leave the rest",
		"3. Do both in parallel",
	}
	longWords = "Ship region #4021 first, then staged UI next week once the rework is green"
	widths    = []int{40, 50, 60}
)

// answerSingle answers the one-question dialog on sim the way answer_session
// does, from a fresh capture.
func answerSingle(t *testing.T, sim *tabbedDialog, reply string) (AnsweredQuestion, error) {
	t.Helper()
	raw, err := sim.Capture()
	if err != nil {
		t.Fatal(err)
	}
	held, ok := dialog.Inspect(ansi.Strip(raw))
	if !ok {
		t.Fatalf("no dialog read off:\n%s", raw)
	}
	return answerHeld(sim, raw, held, reply)
}

func TestAnAnswerNamingAnOptionRegistersThatOption(t *testing.T) {
	fastSettle(t)
	for _, width := range widths {
		for _, option := range rollout.Options {
			t.Run(fmt.Sprintf("%d/%s", width, option.Label), func(t *testing.T) {
				sim := newSingleDialog(rollout, width, plan...)
				answered, err := answerSingle(t, sim, option.Label)
				if err != nil {
					t.Fatalf("answer: %v (keys %v)", err, sim.keys)
				}
				if sim.answers[0] != option.Label || answered.Selected != option.Label || !answered.Verified {
					t.Fatalf("registered %q, reported %q verified=%v (keys %v)",
						sim.answers[0], answered.Selected, answered.Verified, sim.keys)
				}
			})
		}
	}
}

func TestWordsLandVerbatimInTheFreeTextRow(t *testing.T) {
	fastSettle(t)
	for _, width := range widths {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			sim := newSingleDialog(rollout, width, plan...)
			answered, err := answerSingle(t, sim, longWords)
			if err != nil {
				t.Fatalf("answer: %v (keys %v)", err, sim.keys)
			}
			if sim.answers[0] != longWords || answered.Selected != "" || !answered.Verified {
				t.Fatalf("registered %q, selected %q verified=%v", sim.answers[0], answered.Selected, answered.Verified)
			}
		})
	}
}

// The child taking a different option than the one named is an error naming
// both, never a success.
func TestAMisregisteredOptionIsAnError(t *testing.T) {
	fastSettle(t)
	for _, width := range widths {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			sim := newSingleDialog(rollout, width, plan...)
			sim.shift = 1
			answered, err := answerSingle(t, sim, "Hold for staged UI")
			if !errors.Is(err, errWrongAnswer) {
				t.Fatalf("err = %v, want a wrong-answer error; answered %+v", err, answered)
			}
			for _, want := range []string{`"Hold for staged UI"`, `"Only region #4021"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %s", err, want)
				}
			}
			if answered.Verified {
				t.Error("verified a wrong answer")
			}
		})
	}
}

func TestAnAnswerThatCannotBeReadBackIsAnError(t *testing.T) {
	fastSettle(t)
	sim := newSingleDialog(rollout, 50, plan...)
	sim.silent = true
	_, err := answerSingle(t, sim, "Do both")
	if err == nil || !strings.Contains(err.Error(), "cannot be confirmed") {
		t.Fatalf("err = %v, want one saying the answer cannot be confirmed", err)
	}
}

func TestFillDialogReadsEveryAnswerBackAtEveryWidth(t *testing.T) {
	fastSettle(t)
	asked := []convo.AskQuestion{rollout, notify}
	for _, width := range widths {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			sim := newTabbedDialog(asked)
			sim.width, sim.prose = width, plan
			answered, err := fillDialog(sim, questionsOf(t, sim, asked), []QuestionAnswer{
				{Question: "Rollout", Answer: "Do both"},
				{Question: "Notify", Answer: "Only after the deploy is verified in prod, then post"},
			}, true)
			if err != nil {
				t.Fatalf("fillDialog: %v (keys %v)", err, sim.keys)
			}
			if !sim.closed || !answered.Submitted || !answered.Verified {
				t.Fatalf("closed=%v submitted=%v verified=%v", sim.closed, answered.Submitted, answered.Verified)
			}
			if sim.answers[0] != "Do both" || sim.answers[1] != "Only after the deploy is verified in prod, then post" {
				t.Errorf("dialog holds %q", sim.answers)
			}
		})
	}
}

// A tab that ticks with the wrong choice is caught on the review page, and
// the dialog is left unsent for somebody to correct.
func TestFillDialogRefusesToSubmitAMisregisteredAnswer(t *testing.T) {
	fastSettle(t)
	asked := []convo.AskQuestion{rollout, notify}
	for _, width := range widths {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			sim := newTabbedDialog(asked)
			sim.width, sim.shift = width, 1
			_, err := fillDialog(sim, questionsOf(t, sim, asked), []QuestionAnswer{
				{Question: "1", Answer: "Hold for staged UI"}, {Question: "2", Answer: "No"},
			}, true)
			if !errors.Is(err, errWrongAnswer) || !strings.Contains(err.Error(), `registered "Only region #4021"`) {
				t.Fatalf("err = %v", err)
			}
			if sim.closed {
				t.Error("submitted a dialog holding a wrong answer")
			}
		})
	}
}

// answer on one tab of several reads the review page back and returns to
// the question still standing, where the dialog would have gone by itself.
func TestOneAnswerOnATabReadsBackAndReturnsToTheNextQuestion(t *testing.T) {
	fastSettle(t)
	sim := newTabbedDialog([]convo.AskQuestion{rollout, notify})
	sim.width, sim.prose = 50, plan
	answered, err := answerSingle(t, sim, "Only region #4021")
	if err != nil {
		t.Fatalf("answer: %v (keys %v)", err, sim.keys)
	}
	if sim.answers[0] != "Only region #4021" || !answered.Verified || answered.Standing != 1 || answered.Submitted {
		t.Fatalf("answers %q verified=%v standing=%d submitted=%v", sim.answers, answered.Verified, answered.Standing, answered.Submitted)
	}
	if sim.active != 1 || sim.closed {
		t.Errorf("dialog left on tab %d closed=%v, want question 2", sim.active, sim.closed)
	}
	answered, err = answerSingle(t, sim, "No")
	if err != nil || !answered.Submitted || !answered.Verified || !sim.closed {
		t.Fatalf("second answer: %v submitted=%v verified=%v closed=%v", err, answered.Submitted, answered.Verified, sim.closed)
	}
}

// replayPane shows one capture until a key is sent, then the next.
type replayPane struct {
	frames []string
	keys   []string
}

func (p *replayPane) Capture() (string, error) { return p.frames[0], nil }
func (p *replayPane) Keys(keys ...string) error {
	p.keys = append(p.keys, keys...)
	if len(p.frames) > 1 {
		p.frames = p.frames[1:]
	}
	return nil
}
func (p *replayPane) Type(text string) error { return p.Keys("paste:" + text) }

func dialogFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Captured from a live child: "Do both" answered, "Hold for staged UI" taken.
// The readback turns that into an error naming both.
func TestAMisregistrationCapturedLiveIsAnError(t *testing.T) {
	fastSettle(t)
	pane := &replayPane{frames: []string{
		dialogFixture(t, "claude-2.1.284-w50-prose-duplicates.ansi"),
		dialogFixture(t, "claude-2.1.284-w50-typed-answer-took-highlighted.ansi"),
	}}
	raw, _ := pane.Capture()
	_, err := answerHeld(pane, raw, mustInspect(t, raw), "Do both")
	if !errors.Is(err, errWrongAnswer) || !strings.Contains(err.Error(), `answered "Do both", but the child registered "Hold for staged UI"`) {
		t.Fatalf("err = %v", err)
	}
	if fmt.Sprint(pane.keys) != "[Down Down Enter]" {
		t.Errorf("keyed %v", pane.keys)
	}
}

func TestAnAnswerCapturedLiveReadsBack(t *testing.T) {
	fastSettle(t)
	for _, width := range []string{"w50", "w60"} {
		pane := &replayPane{frames: []string{
			dialogFixture(t, "claude-2.1.284-"+width+"-prose-single.ansi"),
			dialogFixture(t, "claude-2.1.284-"+width+"-single-after.ansi"),
		}}
		raw, _ := pane.Capture()
		answered, err := answerHeld(pane, raw, mustInspect(t, raw), "Do both")
		if err != nil || !answered.Verified || answered.Selected != "Do both" {
			t.Fatalf("%s: %v %+v", width, err, answered)
		}
	}
	pane := &replayPane{frames: []string{
		dialogFixture(t, "claude-2.1.284-w40-prose-single.ansi"),
		dialogFixture(t, "claude-2.1.284-w40-on-free-text.ansi"),
		dialogFixture(t, "claude-2.1.284-w40-single-free-after.ansi"),
	}}
	raw, _ := pane.Capture()
	answered, err := answerHeld(pane, raw, mustInspect(t, raw), longWords)
	if err != nil || !answered.Verified || answered.Selected != "" {
		t.Fatalf("w40 words: %v %+v", err, answered)
	}
	if fmt.Sprint(pane.keys) != "[Down Down Down paste:"+longWords+"]" {
		t.Errorf("keyed %v", pane.keys)
	}
}

// A path wider than the record's text column is broken inside a word at 40
// columns, and the child's record then reads "/tmp/s" over "ample-...". The
// answer landed; the readback has to say so, and still catch a wrong one.
func TestAnAnswerToAQuestionWhosePathWrapsMidWordIsVerified(t *testing.T) {
	fastSettle(t)
	for _, width := range widths {
		approval := convo.AskQuestion{Header: "Approval",
			Question: fmt.Sprintf("May I delete the directory /tmp/sample-9000/gie/work/cache-w%d?", width),
			Options:  []convo.AskOption{{Label: "Approve", Description: "Delete it"}, {Label: "Deny", Description: "Keep it"}}}
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			sim := newSingleDialog(approval, width)
			answered, err := answerSingle(t, sim, "Deny")
			if err != nil || !answered.Verified || sim.answers[0] != "Deny" {
				t.Fatalf("answered %+v, err %v, registered %q", answered, err, sim.answers[0])
			}
			wrong := newSingleDialog(approval, width)
			wrong.shift = 1
			_, err = answerSingle(t, wrong, "Approve")
			if !errors.Is(err, errWrongAnswer) || !strings.Contains(err.Error(), `"Deny"`) {
				t.Fatalf("a wrong answer across the wrap read back as %v", err)
			}
		})
	}
	sim := newSingleDialog(convo.AskQuestion{Header: "Approval",
		Question: "May I delete the directory /tmp/sample-9000/gie/work/cache-w40?",
		Options:  []convo.AskOption{{Label: "Approve"}, {Label: "Deny"}}}, 40)
	sim.closed, sim.answers[0] = true, "Deny"
	record, _ := sim.Capture()
	if !strings.Contains(record, "/tmp/s\n     ample-9000") {
		t.Fatalf("the model did not break the path mid-word:\n%s", record)
	}
}
