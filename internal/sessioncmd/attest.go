package sessioncmd

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// Relayed approvals by message.
//
// A child that holds an Approval dialog gets its user's answer through
// answer_session relay: true, keyed into its own dialog (relay.go). That is
// the preferred path: the answer lands as the child's own tool_result and its
// PostToolUse hook tells the classifier whose answer it was.
//
// Not every approval has a dialog to land in. The parent may have asked its
// user before the child reached the step, or the child's dialog is gone. For
// those, send_session with relay_question attaches an attestation to the
// message. Gate Inbox finds the question in the parent's own transcript,
// answered by its user rather than typed by Gate Inbox. The answer must come
// after the child was spawned, within attestFreshness, and never have been
// relayed before. The same ledger spends it that answer_session's relay uses,
// so one answer of the user's approves one thing, whichever path carried it.
// The child's hook spends the attestation's nonce when the message is
// submitted, so a replayed copy of the message approves nothing.

// attestFreshness bounds how old the user's answer may be when it is relayed.
// An approval is for an action at the time it was given; one from an hour ago
// is a question to ask again.
const attestFreshness = 30 * time.Minute

// SendAttested queues message for the caller's child with the user's answer
// to question attached as a relay attestation.
func (s *Sessions) SendAttested(sessionID, targetID, message, subject string, interrupt bool,
	question string) (result SendResult, err error) {
	op := start("sessioncmd.send_attested", sessionAttr(targetID))
	defer func() { op.done(&err) }()
	message, subject, err = checkMessage(message, subject, maxMessageBytes)
	if err != nil {
		return SendResult{}, err
	}
	if normalise(question) == "" {
		return SendResult{}, errors.New("relay_question is empty; give the question you asked your user, word for word")
	}
	runtime, err := s.open()
	if err != nil {
		return SendResult{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return SendResult{}, err
	}
	target, err := runtime.agent(targetID)
	if err != nil {
		return SendResult{}, err
	}
	ask, asked, err := s.attestable(runtime.store, caller, target, question, time.Now())
	if err != nil {
		return SendResult{}, err
	}
	result, err = runtime.enqueue(sender{callerID: caller.ID, id: caller.ID, name: caller.Name}, target.ID,
		message, subject, interrupt)
	if err != nil {
		return SendResult{}, err
	}
	nonce := rand.Text()
	withdraw := func(cause error) (SendResult, error) {
		if dropErr := runtime.store.MarkDropped(result.MessageID, time.Now()); dropErr != nil {
			logging.Warn("could not withdraw a message whose attestation failed", "message", result.MessageID,
				logging.Err(dropErr))
		}
		return SendResult{}, cause
	}
	answer, _ := answerFor(ask.Answers, asked.Question)
	id, err := runtime.store.RecordAnswer(store.DialogAnswer{
		TargetSession:     target.ID,
		TargetToolUseID:   "attest:" + nonce,
		QuestionHash:      questionHash(asked),
		Answer:            answer,
		BySession:         caller.ID,
		Mode:              store.AnswerRelayedUser,
		EvidenceToolUseID: ask.ToolUseID,
	})
	if errors.Is(err, store.ErrEvidenceUsed) {
		return withdraw(fmt.Errorf("%w: your user's answer to that question has already been relayed once; ask "+
			"them again", errRelayRefused))
	}
	if err != nil {
		return withdraw(fmt.Errorf("cannot record the relay, so the message was withdrawn: %w", err))
	}
	if err := runtime.store.MarkAnswer(id, store.AnswerKeyed); err != nil {
		logging.Warn("could not mark a relay attestation's ledger row", "id", id, logging.Err(err))
	}
	options := make([]string, len(asked.Options))
	for i, option := range asked.Options {
		options[i] = option.Label
	}
	if err := runtime.store.RecordAttestation(store.Attestation{
		Nonce:             nonce,
		MessageID:         result.MessageID,
		TargetSession:     target.ID,
		BySession:         caller.ID,
		EvidenceToolUseID: ask.ToolUseID,
		Header:            asked.Header,
		Question:          asked.Question,
		Options:           options,
		Answer:            answer,
		AnsweredAt:        ask.AnsweredAt,
	}); err != nil {
		return withdraw(fmt.Errorf("cannot record the attestation, so the message was withdrawn: %w", err))
	}
	result.Attestation = nonce
	return result, nil
}

// attestable finds the parent's own dialog, answered by its user, that asked
// question, and refuses every case the attestation must not cover.
func (s *Sessions) attestable(st *store.Store, caller, target store.Session, question string,
	now time.Time) (convo.AnsweredAsk, convo.AskQuestion, error) {
	refuse := func(format string, args ...any) (convo.AnsweredAsk, convo.AskQuestion, error) {
		return convo.AnsweredAsk{}, convo.AskQuestion{},
			fmt.Errorf("%w: "+format, append([]any{errRelayRefused}, args...)...)
	}
	if store.TrackerOf(target) != caller.ID {
		return refuse("session %s is not a child you spawned and track, so you cannot relay your user's "+
			"approval to it", target.ID)
	}
	if call, ok := s.pendingCall(target); ok {
		for _, q := range call.Questions {
			if dialog.IsApproval(q.Header) {
				return refuse("session %s is holding its own Approval dialog; put that question to your "+
					"user verbatim and answer it with answer_session relay: true", target.ID)
			}
		}
	}
	if !asks.Located(s.askTarget(caller)) {
		return refuse("this session's own transcript cannot be found, so there is no dialog of your user's to cite")
	}
	answeredAsks, err := asks.AnsweredSince(s.askTarget(caller), time.Time{})
	if err != nil {
		return refuse("cannot read this session's own transcript: %v", err)
	}
	reason := ""
	note := func(r string) {
		if reason == "" {
			reason = r
		}
	}
	for i := len(answeredAsks) - 1; i >= 0; i-- {
		ask := answeredAsks[i]
		for _, q := range ask.Questions {
			if normalise(q.Question) != normalise(question) {
				continue
			}
			_, answered := answerFor(ask.Answers, q.Question)
			switch {
			case !answered:
				note("your dialog asking it holds no answer to it")
			case !target.CreatedAt.IsZero() && !ask.AnsweredAt.After(target.CreatedAt):
				note("your user answered that question before the child was spawned; ask them again")
			case now.Sub(ask.AnsweredAt) > attestFreshness:
				note(fmt.Sprintf("your user answered that question more than %s ago; ask them again", attestFreshness))
			default:
				typed, err := st.AgentAnswered(caller.ID, ask.ToolUseID)
				if err != nil {
					return refuse("cannot read the answer ledger: %v", err)
				}
				if typed {
					note("that answer in your own dialog was typed by the session that spawned you, not by your user")
					continue
				}
				used, err := st.EvidenceUsed(ask.ToolUseID, questionHash(q))
				if err != nil {
					return refuse("cannot read the answer ledger: %v", err)
				}
				if used {
					note("your user's answer to that question has already been relayed once; ask them again")
					continue
				}
				return ask, q, nil
			}
		}
	}
	if reason == "" {
		reason = "no dialog of yours asks that question word for word; ask your user with your own question tool " +
			"and pass the question text exactly as you asked it"
	}
	return refuse("%s", reason)
}
