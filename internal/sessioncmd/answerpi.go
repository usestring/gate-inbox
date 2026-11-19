package sessioncmd

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// piDriver answers the permission dialog the board's pi extension draws. It
// is drawn in Claude Code's shape and answered the same way, by its choice's
// text through answerScreen, so a parent's answer goes through the one relay
// check and ledger every Claude child's prompt does. The call it shows is
// read back from the extension's pending record (pendingToolStrings), so a
// command the pane wraps still matches what the parent put to its user.
//
// The extension's AskUserQuestion dialog is drawn in Claude Code's question
// shape too, so a pane showing it, or its Submit page, is answered by the
// Claude question path (errAskShaped). Anything else is refused rather than
// handed to that path, whose keys would land in pi's editor.
type piDriver struct{}

func init() { registerAnswerDriver("pi", piDriver{}) }

var errPiNoDialog = errors.New("pi is not showing a permission dialog or a question; call read_session to see what it is showing")

func (piDriver) answer(r *runtime, s *Sessions, target store.Session, raw string, answers []QuestionAnswer,
	submit bool, guard *answerGuard, by, byID string) (AnsweredQuestion, error) {
	if askShaped(raw) {
		return AnsweredQuestion{}, errAskShaped
	}
	pane := tmuxPane{driver: r.driver, id: target.ID, record: guard.recorder()}
	return r.answerPi(target, pane, raw, answers[0].reply(), by, byID, guard)
}

func (r *runtime) answerPi(target store.Session, pane dialogPane, raw, reply, by, byID string, guard *answerGuard) (AnsweredQuestion, error) {
	screen, ok := dialog.ReadScreenFor(target.Tool, ansi.Strip(raw))
	if !ok || screen.Kind == dialog.ScreenQuestion || len(screen.Choices) < 2 {
		return AnsweredQuestion{SessionID: target.ID, Name: target.Name}, fmt.Errorf("session %s: %w", target.ID, errPiNoDialog)
	}
	answered, err := r.answerScreen(target, pane, screen.WithExact(guard.pendingStrings()...), reply, by, byID, guard)
	if err != nil {
		return answered, err
	}
	logging.Info(by+" answered a pi child's permission dialog", by, byID, "session", target.ID,
		"selected", answered.Selected, "verified", answered.Verified)
	return answered, nil
}

// askShaped reports whether a pane holds an AskUserQuestion dialog in Claude
// Code's shape: a question under its legend, or the Submit page a dialog
// asking several ends on.
func askShaped(raw string) bool {
	plain := ansi.Strip(raw)
	if held, ok := dialog.Inspect(plain); ok && held.Kind == dialog.KindAsk {
		return true
	}
	_, onReview := dialog.ParseReview(plain)
	return onReview
}
