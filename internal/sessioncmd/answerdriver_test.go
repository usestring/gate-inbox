package sessioncmd

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

type recordingDriver struct {
	got     *[]QuestionAnswer
	submit  *bool
	guarded *bool
}

func (d recordingDriver) answer(_ *runtime, _ *Sessions, target store.Session, raw string, answers []QuestionAnswer,
	submit bool, guard *answerGuard, _, _ string) (AnsweredQuestion, error) {
	*d.got, *d.submit, *d.guarded = answers, submit, guard != nil && guard.target.ID == target.ID
	return AnsweredQuestion{SessionID: target.ID, Verified: true}, nil
}

func TestAToolWithItsOwnDriverIsAnsweredByIt(t *testing.T) {
	h := newSessionHarness(t)
	child := childOn(t, h, h.caller.ID)
	var got []QuestionAnswer
	var submit, guarded bool
	registerAnswerDriver(child.Tool, recordingDriver{&got, &submit, &guarded})
	t.Cleanup(func() {
		driversMu.Lock()
		delete(answerDrivers, child.Tool)
		driversMu.Unlock()
	})

	answered, err := h.sessions.Answer(h.caller.ID, child.ID, "Every storefront", false)
	if err != nil || !answered.Verified || len(got) != 1 || got[0].Question != "" || got[0].Answer != "Every storefront" ||
		!submit || !guarded {
		t.Fatalf("Answer = %+v, %v; driver got %+v submit=%v guarded=%v", answered, err, got, submit, guarded)
	}

	answered, err = h.sessions.AnswerAll(h.caller.ID, child.ID,
		[]QuestionAnswer{{Question: "2", Answer: "Large"}, {Question: "Color", Answer: "Red"}}, false, false)
	if err != nil || len(got) != 2 || got[0].Question != "2" || submit || !guarded {
		t.Fatalf("AnswerAll = %+v, %v; driver got %+v submit=%v", answered, err, got, submit)
	}

	if _, err := h.sessions.Answer(h.caller.ID, "nobody00", "Red", false); err == nil {
		t.Fatal("a session that is not the caller's child was answered through the driver")
	}
}
