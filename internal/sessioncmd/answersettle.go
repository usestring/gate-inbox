package sessioncmd

import (
	"errors"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// One more look before an admitted answer is written off.
//
// An answer is ledgered before its first keystroke and marked failed when the
// handoff errors after that: a readback that came too late -- the child took
// the answer but had not printed or recorded it in time -- or keys a busy pane
// dropped. A failed row used to be the end of it, so the child sat on its
// question while its parent believed it had been told. settle gives
// such an answer one second look: the child's own record, waited on once
// more, marks it keyed when it holds exactly the answer; the same dialog
// still standing is keyed once more; anything else stays failed.

// lateRecordWait is how long settle waits for the child's own record of an
// answer whose readback failed and whose dialog is gone.
var lateRecordWait = 5 * time.Second

// settle is the second look at an answer keyed under g whose handoff ended
// in err. again keys the same answers into the dialog once more. It returns
// the error the answer stands on, nil when it landed after all, and fills
// answered from the retry when there was one.
func (g *answerGuard) settle(pane dialogPane, answered *AnsweredQuestion, err error, again func() (AnsweredQuestion, error)) error {
	if g == nil || err == nil || len(g.rows) == 0 || errors.Is(err, errNothingKeyed) || errors.Is(err, errWrongAnswer) {
		return err
	}
	if g.stillAsking(pane) {
		retried, retryErr := again()
		if retryErr == nil {
			logging.Info("an answer that failed to land was keyed again and landed",
				"session", g.target.ID, "first", err)
			*answered = retried
			return nil
		}
		// A dialog still standing has recorded no answer, so there is no
		// record to wait for.
		if errors.Is(retryErr, errNothingKeyed) {
			return err
		}
		return retryErr
	}
	if g.recordedAsAdmitted(lateRecordWait) {
		logging.Info("an answer whose readback failed is in the child's own record", "session", g.target.ID, "first", err)
		answered.Verified = true
		return nil
	}
	return err
}

// stillAsking is the child still holding the very call this guard admitted
// answers to, on two captures in a row. A dialog that is merely on the screen
// is not enough: keys meant for it land in the child's prompt once it goes.
func (g *answerGuard) stillAsking(pane dialogPane) bool {
	if !g.haveCall || g.call.ToolUseID == "" {
		return false
	}
	for look := range 2 {
		if look > 0 {
			time.Sleep(settlePoll)
		}
		raw, err := pane.Capture()
		if err != nil {
			return false
		}
		_, held := dialog.Inspect(ansi.Strip(raw))
		_, tabbed := dialog.ParseStepper(raw)
		if !held && !tabbed {
			return false
		}
		call, ok := asks.Pending(g.sessions.askTarget(g.target))
		if !ok || call.ID != g.call.ToolUseID {
			return false
		}
	}
	return true
}

// recordedAsAdmitted waits up to wait for the child's own record of its
// pending call and reports whether it holds every answer this guard admitted,
// exactly. A record holding anything else is not waited on.
func (g *answerGuard) recordedAsAdmitted(wait time.Duration) bool {
	record := g.recorder()
	if record == nil {
		return false
	}
	rows, err := g.store.AnswersFor(g.target.ID, g.call.ToolUseID)
	if err != nil {
		return false
	}
	var admitted []store.DialogAnswer
	for _, row := range rows {
		if row.State == store.AnswerPending && row.BySession == g.caller.ID {
			admitted = append(admitted, row)
		}
	}
	if len(admitted) == 0 {
		return false
	}
	deadline := time.Now().Add(wait)
	for {
		if answers, ok := record(); ok {
			return g.holds(answers, admitted)
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(settlePoll)
	}
}

// holds reports whether answers, the child's record, gives each admitted row
// its answer.
func (g *answerGuard) holds(answers map[string]string, admitted []store.DialogAnswer) bool {
	for _, row := range admitted {
		found := false
		for _, q := range g.call.Questions {
			if questionHash(q) != row.QuestionHash {
				continue
			}
			given, ok := answerFor(answers, q.Question)
			found = ok && sameAnswer(given, row.Answer, q.MultiSelect)
			break
		}
		if !found {
			return false
		}
	}
	return true
}

// rekeyHeld is answerHeld from a fresh capture: the retry settle makes of a
// one-question answer.
func rekeyHeld(pane dialogPane, reply string) (AnsweredQuestion, error) {
	raw, err := pane.Capture()
	if err != nil {
		return AnsweredQuestion{}, err
	}
	held, ok := dialog.Inspect(ansi.Strip(raw))
	if !ok {
		return AnsweredQuestion{}, errNothingKeyed
	}
	return answerHeld(pane, raw, held, reply)
}
