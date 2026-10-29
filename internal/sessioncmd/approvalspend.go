package sessioncmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/store"
)

// An action an agent's shell is refused until its user approves it.
//
// Some commands refuse every agent shell, such as a charter setting that picks
// the model judging the agent. Before this, the only way through was the agent
// telling its user to run the command themselves, a question per change. Now
// the command names one Approval question, word for word; the agent asks it
// in its own dialog, a child's parent passes its user's answer into it
// verbatim, and the command spends that answer once. The approval lands in the
// session that acts, where the auto-mode classifier sees it too.
//
// What counts: an AskUserQuestion in the caller's own transcript, headed
// Approval, asking question word for word, answered ApproveOption within
// approvalFreshness, by its user -- one a parent passed on counts, one Gate Inbox
// keyed for an agent does not -- and never spent on another action.

// ApproveOption is the option that approves an action.
const ApproveOption = "Approve"

// approvalFreshness bounds how old an approval may be when it is spent.
const approvalFreshness = 30 * time.Minute

// ErrNotApproved is SpendApproval finding no answer it may spend.
var ErrNotApproved = errors.New("no unspent approval from the user")

// SpendApproval spends the caller's user's approval of question, or reports
// why there is none to spend.
func (s *Sessions) SpendApproval(callerID, question string, now time.Time) (err error) {
	op := start("sessioncmd.spend_approval", sessionAttr(callerID))
	defer func() { op.done(&err) }()
	return s.spendOwnAnswer(callerID, question, ApproveOption, now)
}

// SpendAnswer spends the caller's user's answer to an Approval question, as
// SpendApproval does, for an answer that is one of the question's own options
// rather than ApproveOption: the caller is passing on its user's choice in
// another session's Approval dialog, which it asked its user word for word.
func (s *Sessions) SpendAnswer(callerID, question, answer string, now time.Time) (err error) {
	op := start("sessioncmd.spend_answer", sessionAttr(callerID))
	defer func() { op.done(&err) }()
	if normalise(answer) == "" {
		return fmt.Errorf("%w: no answer", ErrNotApproved)
	}
	return s.spendOwnAnswer(callerID, question, answer, now)
}

func (s *Sessions) spendOwnAnswer(callerID, question, want string, now time.Time) error {
	if callerID == "" || normalise(question) == "" {
		return fmt.Errorf("%w: no session or question", ErrNotApproved)
	}
	runtime, err := s.open()
	if err != nil {
		return err
	}
	defer runtime.store.Close()
	caller, err := runtime.store.Get(callerID)
	if err != nil {
		return err
	}
	_, err = s.spendAnswer(runtime, caller, question, want, now)
	return err
}

// spendAnswer spends the caller's user's answer of option to the Approval
// question and returns the ledger row that spent it, or reports why there is
// none to spend.
func (s *Sessions) spendAnswer(runtime *runtime, caller store.Session, question, option string, now time.Time) (int64, error) {
	answered, err := asks.AnsweredSince(s.askTarget(caller), now.Add(-approvalFreshness))
	if err != nil {
		return 0, err
	}
	reason := "this session's own transcript holds no Approval dialog asking it word for word in the last 30 minutes"
	for i := len(answered) - 1; i >= 0; i-- {
		ask := answered[i]
		for _, q := range ask.Questions {
			if !dialog.IsApproval(q.Header) || normalise(q.Question) != normalise(question) {
				continue
			}
			given, ok := answerFor(ask.Answers, q.Question)
			if !ok || normalise(given) != normalise(option) {
				reason = fmt.Sprintf("the user answered %q, not %q", given, option)
				continue
			}
			typed, err := runtime.store.AgentAnswered(caller.ID, ask.ToolUseID)
			if err != nil {
				return 0, err
			}
			if typed {
				reason = "that answer was keyed by an agent, not given by the user"
				continue
			}
			id, err := runtime.store.RecordAnswer(store.DialogAnswer{
				TargetSession:     caller.ID,
				TargetToolUseID:   ask.ToolUseID,
				QuestionHash:      questionHash(q),
				Answer:            given,
				BySession:         caller.ID,
				Mode:              store.AnswerSpentOnAction,
				EvidenceToolUseID: ask.ToolUseID,
				CreatedAt:         now,
			})
			if errors.Is(err, store.ErrEvidenceUsed) {
				reason = "that approval has already been spent on this action once"
				continue
			}
			return id, err
		}
	}
	return 0, fmt.Errorf("%w: %s", ErrNotApproved, reason)
}
