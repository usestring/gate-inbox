package sessioncmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
)

// recordingPane is a replayPane whose child keeps a transcript record.
type recordingPane struct {
	replayPane
	answers map[string]string
}

func (p *recordingPane) Recorded() (map[string]string, bool) { return p.answers, p.answers != nil }

// The third false "cannot be confirmed" of 2026-10-01, on a live Claude Code
// 2.1.286 pane at 50 columns: a 739-character Approval question whose record
// was taller than the screen, redrawn without its header. The child took
// "Push and open the PR"; its transcript says so.
func TestAnAnswerWhoseRecordScrolledOffIsReadFromTheTranscript(t *testing.T) {
	fastSettle(t)
	dialogRaw := dialogFixture(t, "claude-2.1.286-w50-long-mine-dialog.ansi")
	answeredRaw := dialogFixture(t, "claude-2.1.286-w50-long-mine-answered-screen.ansi")
	held := mustInspect(t, dialogRaw)
	question := "May I push branch s-145287-answer-every-dialog to the PUBLIC repo usestring/gate-inbox and open a " +
		"ready-for-review PR? Exact commands ... The body file's full text is printed in the session just above this question."

	pane := &recordingPane{replayPane{frames: []string{dialogRaw, answeredRaw}}, map[string]string{question: "Push and open the PR"}}
	raw, _ := pane.Capture()
	answered, err := answerHeld(pane, raw, held, "Push and open the PR")
	if err != nil || !answered.Verified || answered.Selected != "Push and open the PR" {
		t.Fatalf("answered %+v, err %v", answered, err)
	}

	wrong := &recordingPane{replayPane{frames: []string{dialogRaw, answeredRaw}}, map[string]string{question: "Push only"}}
	raw, _ = wrong.Capture()
	_, err = answerHeld(wrong, raw, held, "Push and open the PR")
	if !errors.Is(err, errWrongAnswer) || !strings.Contains(err.Error(), `registered "Push only"`) {
		t.Fatalf("a transcript recording another answer read back as %v", err)
	}

	// With no transcript the pane is all there is, and it holds no record to
	// confirm against: that is still said, never guessed.
	bare := &replayPane{frames: []string{dialogRaw, answeredRaw}}
	raw, _ = bare.Capture()
	if _, err = answerHeld(bare, raw, held, "Push and open the PR"); err == nil || !strings.Contains(err.Error(), "cannot be confirmed") {
		t.Fatalf("a pane with no record read back as %v", err)
	}
}

// The first two false negatives: the record was on the screen, but its
// question and the dialog's prompt break the same long paths in different
// places. Both real 50-column records now confirm the answer and refuse the
// other option.
func TestALongQuestionsScreenRecordConfirmsItsAnswer(t *testing.T) {
	fastSettle(t)
	for key, answer := range map[string]string{"dc2": "Yes, push and open the PR", "dc3": "Yes, open it via the wrapper"} {
		dialogRaw := dialogFixture(t, "claude-2.1.286-w50-long-"+key+"-dialog.ansi")
		answeredRaw := dialogFixture(t, "claude-2.1.286-w50-long-"+key+"-answered-screen.ansi")
		held := mustInspect(t, dialogRaw)
		pane := &replayPane{frames: []string{dialogRaw, answeredRaw}}
		raw, _ := pane.Capture()
		if answered, err := answerHeld(pane, raw, held, answer); err != nil || !answered.Verified {
			t.Fatalf("%s: answered %+v, err %v", key, answered, err)
		}
		other := held.Options[1]
		pane = &replayPane{frames: []string{dialogRaw, answeredRaw}}
		raw, _ = pane.Capture()
		if _, err := answerHeld(pane, raw, held, other); !errors.Is(err, errWrongAnswer) {
			t.Fatalf("%s: answering %q against a record of %q read back as %v", key, other, answer, err)
		}
		if len(dialog.ParseAnswered(ansi.Strip(answeredRaw))) != 1 {
			t.Fatalf("%s: fixture record unreadable", key)
		}
	}
}
