package sessioncmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/relayask"
	"github.com/usestring/gate-inbox/internal/store"
)

// Relaying a child's dialog without retyping it: read_session's
// digest.relay_ask, and refusals that carry the call that settles them. The
// input itself is built by package relayask.

// RelayAsk is the input for the caller's own AskUserQuestion that relays a
// child's dialog word for word.
type RelayAsk = relayask.Input

// relayAskOn reads the dialog target's pane is holding into the RelayAsk that
// relays it, or nil when the pane holds no dialog to relay.
func (s *Sessions) relayAskOn(target store.Session, raw string) *RelayAsk {
	asked := s.asked(target)
	if reading, ok := dialog.ReadQuestions(target.Tool, raw, asked); ok && len(reading.Questions) > 0 {
		return relayask.For(asked, reading.Questions)
	}
	screen, ok := dialog.ReadScreenFor(target.Tool, raw)
	if !ok || screen.Kind == dialog.ScreenQuestion {
		return nil
	}
	about := relayask.Screen{Child: target.Name}
	if path := s.transcriptOf(target); path != "" {
		screen = screen.WithExact(convo.PendingToolStrings(path)...)
		about.Description = convo.PendingToolDescription(path)
	}
	return relayask.ForScreen(screen, about)
}

// Answer picks or types reply into the question dialog targetID is holding;
// see answerOne. A refusal comes back carrying the call that settles it.
func (s *Sessions) Answer(sessionID, targetID, reply string, relay bool) (AnsweredQuestion, error) {
	answered, err := s.answerOne(sessionID, targetID, reply, relay)
	return answered, s.nextCall(sessionID, targetID, err)
}

// AnswerAll answers several questions of the dialog targetID is holding; see
// answerAll. A refusal comes back carrying the call that settles it.
func (s *Sessions) AnswerAll(sessionID, targetID string, answers []QuestionAnswer, submit, relay bool) (AnsweredQuestion, error) {
	answered, err := s.answerAll(sessionID, targetID, answers, submit, relay)
	return answered, s.nextCall(sessionID, targetID, err)
}

// nextCall turns a refusal of a parent's answer into the call that settles
// it: a relay refusal carries the RelayAsk to put to the user, and an answer
// to a session on no dialog says what the session is doing and what to wait
// for. Any other error is returned as it is.
func (s *Sessions) nextCall(sessionID, targetID string, err error) error {
	if err == nil || !(errors.Is(err, errRelayRefused) || errors.Is(err, errApprovalNeedsRelay) ||
		errors.Is(err, errScreenNeedsRelay) || noDialogUp(err)) {
		return err
	}
	r, openErr := s.open()
	if openErr != nil {
		return err
	}
	defer r.store.Close()
	caller, callerErr := r.caller(sessionID)
	if callerErr != nil {
		return err
	}
	target, childErr := r.child(caller, targetID)
	if childErr != nil {
		return err
	}
	raw, captureErr := r.driver.CapturePane(target.ID)
	if captureErr != nil {
		raw = ""
	}
	return s.explainRefusal(r, target, raw, err)
}

// noDialogUp is the refusal of an answer to a session showing no dialog at
// all. A Codex or OpenCode question that expired or was dismissed is
// ErrNoDialog too, but waiting will not bring it back, so its own words stand.
func noDialogUp(err error) bool {
	return errors.Is(err, dialog.ErrNoDialog) && strings.Contains(err.Error(), "is not on a dialog this can read")
}

// explainRefusal is nextCall's rewrite of err, given the child's pane as it
// stands now.
func (s *Sessions) explainRefusal(r *runtime, target store.Session, raw string, err error) error {
	var ask *RelayAsk
	if strings.TrimSpace(ansi.Strip(raw)) != "" {
		ask = s.relayAskOn(target, raw)
	}
	switch {
	case errors.Is(err, errRelayRefused) && strings.Contains(err.Error(), "your user answered \""):
		// The user was asked and chose something else: the answer to key is
		// theirs, not a new question.
		return err
	case errors.Is(err, errRelayRefused), errors.Is(err, errApprovalNeedsRelay), errors.Is(err, errScreenNeedsRelay):
		if ask == nil {
			return err
		}
		return wrapped(err, fmt.Sprintf("%v\n\nNext: call your own AskUserQuestion with exactly this input, "+
			"then answer_session on session %s with relay: true and answer set to the option your user picks "+
			"(for several questions, answers with one entry each):\n%s", err, target.ID, ask.JSON()))
	case noDialogUp(err):
		if ask != nil {
			// A dialog came up between the answer's capture and this one.
			return wrapped(err, fmt.Sprintf("%v\n\nA dialog is up now. Next: call your own AskUserQuestion with "+
				"exactly this input, then answer_session on session %s with relay: true:\n%s", err, target.ID, ask.JSON()))
		}
		state := target.Status
		if row, getErr := r.store.Get(target.ID); getErr == nil {
			state = row.Status
		}
		return wrapped(err, fmt.Sprintf("no dialog up: session %s's status is %s. Next: wait_for_session on it "+
			"until it is waiting, then read_session, whose digest.relay_ask is the input to put to your user. (%v)",
			target.ID, r.restingStatus(target.ID, state), err))
	}
	return err
}
