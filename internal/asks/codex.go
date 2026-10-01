package asks

import (
	"time"

	"github.com/usestring/gate-inbox/internal/codexq"
	"github.com/usestring/gate-inbox/internal/convo"
)

const CodexExpiry = 2 * time.Minute

type codex struct{}

func init() { Register("codex", codex{}) }

func (codex) Traits() Traits {
	return Traits{
		Name:     "Codex",
		FreeText: "put in the note of its \"" + codexq.NoneOfTheAbove + "\" row instead",
		Expires:  CodexExpiry,
	}
}

func (codex) path(t Target) string { return codexq.RolloutPath(codexq.Root(), t.AgentSessionID) }

func (c codex) Located(t Target) bool { return c.path(t) != "" }

func (c codex) calls(t Target) []codexq.Call {
	path := c.path(t)
	if path == "" {
		return nil
	}
	calls, _ := codexq.CallsAt(path)
	return calls
}

func codexCall(call codexq.Call) Call {
	out := Call{Tool: "codex", ID: call.CallID, AskedAt: call.AskedAt, Async: call.Async}
	for _, q := range call.Questions {
		asked := convo.AskQuestion{ID: q.ID, Header: q.Header, Question: q.Question}
		for _, option := range q.Options {
			asked.Options = append(asked.Options, convo.AskOption{Label: option.Label, Description: option.Description})
		}
		out.Questions = append(out.Questions, asked)
	}
	return out
}

func codexResult(call codexq.Call) Result {
	result := Result{Call: codexCall(call), At: call.ResolvedAt}
	switch call.State {
	case codexq.Answered:
		result.Outcome = Answered
	case codexq.Expired:
		result.Outcome = Expired
	case codexq.Superseded:
		switch {
		case call.Async && call.Reply != "":
			result.Outcome, result.At = Answered, call.ReplyAt
			result.Reply = call.Reply
		case call.ResolvedAt.IsZero():
			result.Outcome = Unresolved
		default:
			result.Outcome = Expired
		}
	}
	for _, q := range call.Questions {
		given := call.Answers[q.ID]
		result.Answers = append(result.Answers, Registered{Labels: given.Labels, Note: given.Note})
	}
	return result
}

func (c codex) Pending(t Target) (Call, bool) {
	calls := c.calls(t)
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].State == codexq.Outstanding && len(calls[i].Questions) > 0 {
			return codexCall(calls[i]), true
		}
	}
	return Call{}, false
}

func (c codex) Result(t Target, id string) (Result, bool) {
	for _, call := range c.calls(t) {
		if call.CallID == id {
			return codexResult(call), true
		}
	}
	return Result{}, false
}

func (c codex) Unanswered(t Target) (Result, bool) {
	calls := c.calls(t)
	if len(calls) == 0 {
		return Result{}, false
	}
	last := calls[len(calls)-1]
	if last.State != codexq.Expired {
		return Result{}, false
	}
	return codexResult(last), true
}

func (c codex) Answered(t Target, since time.Time) ([]convo.AnsweredAsk, error) {
	var out []convo.AnsweredAsk
	for _, call := range c.calls(t) {
		if call.State != codexq.Answered || !call.ResolvedAt.After(since) {
			continue
		}
		asked := codexCall(call)
		answered := convo.AnsweredAsk{ToolUseID: call.CallID, Questions: asked.Questions, AskedAt: call.AskedAt,
			AnsweredAt: call.ResolvedAt, Answers: map[string]string{}}
		for _, q := range call.Questions {
			given, ok := call.Answers[q.ID]
			if !ok {
				continue
			}
			answered.Answers[q.Question] = codexAnswerText(given)
		}
		out = append(out, answered)
	}
	return out, nil
}

func codexAnswerText(given codexq.Answer) string {
	if len(given.Labels) == 1 && given.Labels[0] == codexq.NoneOfTheAbove && given.Note != "" {
		return given.Note
	}
	if len(given.Labels) > 0 {
		return given.Labels[0]
	}
	return given.Note
}
