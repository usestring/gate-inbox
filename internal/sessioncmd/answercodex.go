package sessioncmd

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

type codexDriver struct{}

func init() { registerAnswerDriver("codex", codexDriver{}) }

func (codexDriver) answer(r *runtime, s *Sessions, target store.Session, raw string, answers []QuestionAnswer,
	submit bool, guard *answerGuard, by, byID string) (AnsweredQuestion, error) {
	if screen, ok := dialog.ReadScreenFor(target.Tool, raw); ok && screen.Kind != dialog.ScreenQuestion && len(screen.Choices) >= 2 {
		return r.answerScreen(target, tmuxPane{driver: r.driver, id: target.ID}, screen, answers[0].reply(), by, byID, guard)
	}
	t := s.askTarget(target)
	call, pending := asks.Pending(t)
	record := func(id string) (asks.Result, bool) { return asks.ResultOf(t, id) }
	lost := func() (asks.Result, bool) { return asks.Unanswered(t) }
	answered, err := answerCodex(tmuxPane{driver: r.driver, id: target.ID}, call, pending, record, lost, raw, answers, submit, guard)
	answered.SessionID, answered.Name = target.ID, target.Name
	if pending {
		answered.readFrom = "Codex's rollout"
	}
	if err != nil {
		logging.Warn(by+"'s answer to a Codex child's question did not land as given",
			by, byID, "session", target.ID, "err", err)
		return answered, fmt.Errorf("session %s: %w", target.ID, err)
	}
	logging.Info(by+" answered a Codex child's question", by, byID, "session", target.ID,
		"answers", len(answered.Answers), "submitted", answered.Submitted, "verified", answered.Verified)
	return answered, nil
}

type codexStep struct {
	index    int
	answer   string
	option   int
	question dialog.Question
}

func answerCodex(pane dialogPane, call asks.Call, pending bool, record func(string) (asks.Result, bool),
	lost func() (asks.Result, bool), raw string, answers []QuestionAnswer, submit bool, guard *answerGuard) (AnsweredQuestion, error) {
	if pending && call.Async {
		return answerCodexAsync(pane, call, record, answers, guard)
	}
	ask, ok := dialog.ParseCodexAsk(raw)
	if !ok {
		return codexNotAQuestion(pane, raw, call, pending, record, lost, answers)
	}
	var asked []convo.AskQuestion
	if pending {
		asked = call.Questions
	}
	reading, _ := dialog.ReadQuestions("codex", raw, asked)
	questions := reading.Questions
	result := AnsweredQuestion{Questions: questions}
	steps, err := planCodex(questions, ask, answers)
	if err != nil {
		return result, err
	}
	planned := make([]plannedAnswer, len(steps))
	for i, step := range steps {
		planned[i] = plannedAnswer{step.index, step.answer}
	}
	if guard != nil {
		if err := guard.admit(planned, questions); err != nil {
			return result, err
		}
	}
	result, err = fillCodex(pane, questions, steps)
	if err == nil && result.Submitted && pending {
		err = readbackCodex(call, record, steps, &result)
	}
	guard.finish(err)
	return result, err
}

func planCodex(questions []dialog.Question, ask dialog.CodexAsk, answers []QuestionAnswer) ([]codexStep, error) {
	var steps []codexStep
	seen := map[int]bool{}
	for _, answer := range answers {
		index := ask.Index - 1
		if strings.TrimSpace(answer.Question) != "" {
			var err error
			if index, err = dialog.Resolve(questions, answer.Question); err != nil {
				return nil, err
			}
		}
		if index < 0 || index >= len(questions) {
			return nil, fmt.Errorf("question %d is not one of the dialog's %d", index+1, len(questions))
		}
		if seen[index] {
			return nil, fmt.Errorf("question %d is answered twice in answers", index+1)
		}
		seen[index] = true
		text := strings.TrimSpace(answer.Answer)
		question := questions[index]
		steps = append(steps, codexStep{index: index, answer: text, option: question.Choose(text), question: question})
	}
	slices.SortFunc(steps, func(a, b codexStep) int { return a.index - b.index })
	return steps, nil
}

// sameCodexPrompt is the question on the screen read against the one in the
// rollout. Codex wraps a long word at a slash or hyphen without a space, so
// the screen's rows, joined with spaces, can split a path the rollout holds
// whole.
func sameCodexPrompt(screen, asked string) bool {
	squash := func(text string) string { return strings.Join(strings.Fields(text), "") }
	return dialog.SameText(screen, asked) || dialog.SameText(squash(screen), squash(asked))
}

func fillCodex(pane dialogPane, questions []dialog.Question, steps []codexStep) (AnsweredQuestion, error) {
	result := AnsweredQuestion{Questions: questions}
	for _, step := range steps {
		raw, err := codexNavigate(pane, step.index)
		if err != nil {
			return codexFinish(pane, result, err)
		}
		ask, _ := dialog.ParseCodexAsk(raw)
		if step.question.Question != "" && !sameCodexPrompt(ask.Prompt, step.question.Question) {
			return codexFinish(pane, result, fmt.Errorf("%w: question %d on the screen reads %q, not %q",
				errDialogMoved, step.index+1, ask.Prompt, step.question.Question))
		}
		filled := FilledAnswer{Index: step.index + 1, Header: step.question.Header, Answer: step.answer,
			question: step.question.Question}
		if step.option > 0 {
			filled.Selected = step.question.Options[step.option-1].Label
			err = codexPick(pane, ask, filled.Selected)
		} else {
			err = codexType(pane, ask, step.answer, step.index+1)
		}
		if err != nil {
			return codexFinish(pane, result, err)
		}
		after, err := waitFor(pane, func(raw string) bool {
			next, ok := dialog.ParseCodexAsk(raw)
			if !ok {
				return true
			}
			return next.Index != ask.Index || next.Unanswered != ask.Unanswered && !next.NotesOpen
		})
		if err != nil {
			return codexFinish(pane, result, fmt.Errorf("question %d was keyed but the dialog did not move on: %w",
				step.index+1, err))
		}
		result.Answers = append(result.Answers, filled)
		if _, goBack, cursor, confirm := dialog.CodexUnansweredConfirm(after); confirm {
			if err := codexChoose(pane, cursor, goBack); err != nil {
				return codexFinish(pane, result, err)
			}
			if _, err := waitFor(pane, func(raw string) bool {
				_, ok := dialog.ParseCodexAsk(raw)
				return ok
			}); err != nil {
				return codexFinish(pane, result, fmt.Errorf("%w: the dialog did not come back from its "+
					"submit confirmation", errDialogMoved))
			}
			continue
		}
		if _, still := dialog.ParseCodexAsk(after); !still {
			result.Submitted = true
			break
		}
	}
	return codexFinish(pane, result, nil)
}

func codexFinish(pane dialogPane, result AnsweredQuestion, err error) (AnsweredQuestion, error) {
	if len(result.Answers) > 0 {
		last := result.Answers[len(result.Answers)-1]
		result.Answer, result.Selected = last.Answer, last.Selected
		result.Question = last.question
	}
	if raw, captureErr := pane.Capture(); captureErr == nil && !result.Submitted {
		if ask, ok := dialog.ParseCodexAsk(raw); ok {
			result.Standing = max(ask.Unanswered, 0)
		}
	}
	for _, filled := range result.Answers {
		if filled.Index-1 < len(result.Questions) {
			result.Questions[filled.Index-1].Answered = true
			result.Questions[filled.Index-1].Answer = filled.Answer
		}
	}
	return result, err
}

func codexNavigate(pane dialogPane, index int) (string, error) {
	for range 16 {
		raw, err := pane.Capture()
		if err != nil {
			return "", err
		}
		ask, ok := dialog.ParseCodexAsk(raw)
		if !ok {
			if _, goBack, cursor, confirm := dialog.CodexUnansweredConfirm(raw); confirm {
				if err := codexChoose(pane, cursor, goBack); err != nil {
					return "", err
				}
				continue
			}
			return "", fmt.Errorf("%w: the question dialog is gone -- it was answered, cancelled or expired", errDialogMoved)
		}
		if ask.NotesOpen && strings.TrimSpace(ask.Notes) != "" {
			return "", fmt.Errorf("%w: somebody is typing a note into question %d", errDialogMoved, ask.Index)
		}
		if ask.Index-1 == index {
			return raw, nil
		}
		key := "Right"
		if index < ask.Index-1 {
			key = "Left"
		}
		from := ask.Index
		if err := pane.Keys(key); err != nil {
			return "", err
		}
		if _, err := waitFor(pane, func(raw string) bool {
			moved, ok := dialog.ParseCodexAsk(raw)
			return !ok || moved.Index != from
		}); err != nil {
			return "", fmt.Errorf("the dialog did not move off question %d", from)
		}
	}
	return "", fmt.Errorf("could not reach question %d", index+1)
}

func codexPick(pane dialogPane, ask dialog.CodexAsk, label string) error {
	n := 0
	for _, option := range ask.Options {
		if dialog.SameText(option.Label, label) {
			n = option.Number
		}
	}
	if n == 0 {
		return fmt.Errorf("%w: question %d shows no option %q", errDialogMoved, ask.Index, label)
	}
	return codexChoose(pane, ask.Cursor, n)
}

func codexChoose(pane dialogPane, cursor, n int) error {
	if n >= 1 && n <= 9 {
		return pane.Keys(strconv.Itoa(n))
	}
	keys := dialog.SelectKeys(cursor, n)
	if keys == nil {
		return errors.New("cannot locate the dialog's cursor, so nothing was keyed")
	}
	return pane.Keys(keys...)
}

func codexType(pane dialogPane, ask dialog.CodexAsk, text string, index int) error {
	other := ask.Other()
	if other == 0 {
		return fmt.Errorf("question %d draws no %q row to write %q under; answer with one of its options",
			index, dialog.CodexNoneOfTheAbove, text)
	}
	if ask.Cursor == 0 {
		return fmt.Errorf("question %d's cursor cannot be located, so nothing was keyed", index)
	}
	if ask.Cursor != other {
		keys := dialog.SelectKeys(ask.Cursor, other)
		if err := pane.Keys(keys[:len(keys)-1]...); err != nil {
			return err
		}
		if _, err := waitFor(pane, func(raw string) bool {
			moved, ok := dialog.ParseCodexAsk(raw)
			return ok && moved.Cursor == other
		}); err != nil {
			return fmt.Errorf("%w: the cursor never reached question %d's %q row", errDialogMoved, index, dialog.CodexNoneOfTheAbove)
		}
	}
	if err := pane.Keys("Tab"); err != nil {
		return err
	}
	if _, err := waitFor(pane, func(raw string) bool {
		moved, ok := dialog.ParseCodexAsk(raw)
		return ok && moved.NotesOpen
	}); err != nil {
		return fmt.Errorf("%w: question %d's note did not open", errDialogMoved, index)
	}
	return pane.Type(text)
}

func readbackCodex(call asks.Call, record func(string) (asks.Result, bool), steps []codexStep, result *AnsweredQuestion) error {
	deadline := time.Now().Add(readbackTimeout)
	var got asks.Result
	for {
		var ok bool
		got, ok = record(call.ID)
		if ok && got.Outcome != asks.Unresolved {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Codex has not recorded the answers to its call in its rollout, so they cannot be " +
				"confirmed; read_session shows where it stands")
		}
		time.Sleep(settlePoll)
	}
	if got.Outcome == asks.Expired {
		return fmt.Errorf("%w: Codex resolved the question by itself with no answer before the answers landed; "+
			"send it the answer with send_session", errWrongAnswer)
	}
	for _, step := range steps {
		if step.index >= len(got.Answers) {
			return fmt.Errorf("Codex's record of the call has no answer for question %d", step.index+1)
		}
		registered := got.Answers[step.index]
		want := FilledAnswer{Index: step.index + 1, Answer: step.answer}
		if step.option > 0 {
			want.Selected = step.question.Options[step.option-1].Label
			if len(registered.Labels) != 1 || !dialog.SameText(registered.Labels[0], want.Selected) || registered.Note != "" {
				return mismatch(want, registered.Text(), "the child has already taken it; send it a correction")
			}
			continue
		}
		if len(registered.Labels) != 1 || registered.Labels[0] != dialog.CodexNoneOfTheAbove ||
			!dialog.SameText(registered.Note, step.answer) {
			return mismatch(want, registered.Text(), "the child has already taken it; send it a correction")
		}
	}
	result.Verified = true
	return nil
}

func answerCodexAsync(pane dialogPane, call asks.Call, record func(string) (asks.Result, bool), answers []QuestionAnswer,
	guard *answerGuard) (AnsweredQuestion, error) {
	questions := make([]dialog.Question, len(call.Questions))
	for i, q := range call.Questions {
		questions[i] = dialog.Question{Index: i + 1, ID: q.ID, Header: q.Header, Question: q.Question}
		for _, option := range q.Options {
			questions[i].Options = append(questions[i].Options, dialog.Option{Label: option.Label})
		}
	}
	result := AnsweredQuestion{Questions: questions}
	var lines []string
	planned := make([]plannedAnswer, 0, len(answers))
	for _, answer := range answers {
		index := 0
		if strings.TrimSpace(answer.Question) != "" {
			var err error
			if index, err = dialog.Resolve(questions, answer.Question); err != nil {
				return result, err
			}
		}
		if index >= len(questions) {
			return result, fmt.Errorf("the call asks %d questions", len(questions))
		}
		text := strings.TrimSpace(answer.Answer)
		planned = append(planned, plannedAnswer{index, text})
		line := text
		if len(questions) > 1 {
			line = questions[index].Question + " -- " + text
		}
		lines = append(lines, line)
		result.Answers = append(result.Answers, FilledAnswer{Index: index + 1, Header: questions[index].Header, Answer: text})
	}
	if guard != nil {
		if err := guard.admit(planned, questions); err != nil {
			return result, err
		}
	}
	message := strings.Join(lines, "\n")
	err := pane.Type(message)
	if err == nil {
		err = readbackAsync(call, record, message, &result)
	}
	guard.finish(err)
	if last := len(result.Answers) - 1; last >= 0 {
		result.Question, result.Answer = questions[result.Answers[last].Index-1].Question, result.Answers[last].Answer
	}
	return result, err
}

func readbackAsync(call asks.Call, record func(string) (asks.Result, bool), message string, result *AnsweredQuestion) error {
	deadline := time.Now().Add(readbackTimeout)
	for {
		got, ok := record(call.ID)
		if ok && got.Outcome == asks.Answered {
			if !strings.Contains(strings.Join(strings.Fields(got.Reply), " "), strings.Join(strings.Fields(message), " ")) {
				return fmt.Errorf("%w: the answer was sent as %q, but Codex recorded the message %q", errWrongAnswer,
					message, got.Reply)
			}
			result.Verified, result.Submitted = true, true
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the answer was typed as a message but Codex has not recorded it yet; read_session shows " +
				"whether it arrived")
		}
		time.Sleep(settlePoll)
	}
}

func codexNotAQuestion(pane dialogPane, raw string, call asks.Call, pending bool, record func(string) (asks.Result, bool),
	lost func() (asks.Result, bool), answers []QuestionAnswer) (AnsweredQuestion, error) {
	plain := ansi.Strip(raw)
	if proceed, goBack, cursor, ok := dialog.CodexUnansweredConfirm(plain); ok {
		if len(answers) == 1 && isSubmitWord(answers[0].Answer) {
			if err := codexChoose(pane, cursor, proceed); err != nil {
				return AnsweredQuestion{}, err
			}
			answered := AnsweredQuestion{Question: "Submit with unanswered questions?", Answer: answers[0].Answer,
				Selected: "Proceed", Submitted: true}
			if !pending {
				return answered, nil
			}
			deadline := time.Now().Add(readbackTimeout)
			for {
				if got, ok := record(call.ID); ok && got.Outcome == asks.Answered {
					answered.Verified = true
					return answered, nil
				} else if ok && got.Outcome == asks.Expired {
					return answered, fmt.Errorf("%w: Codex resolved the question by itself with no answer", errWrongAnswer)
				}
				if time.Now().After(deadline) {
					return answered, errors.New("Submit was pressed but Codex has not recorded the answers yet")
				}
				time.Sleep(settlePoll)
			}
		}
		if err := codexChoose(pane, cursor, goBack); err != nil {
			return AnsweredQuestion{}, err
		}
		return AnsweredQuestion{}, fmt.Errorf("%w: the child was on Codex's \"Submit with unanswered questions?\" "+
			"page; it was sent back to its first unanswered question, so call again with the answers, or answer "+
			"\"Submit answers\" to submit with questions left unanswered", errDialogMoved)
	}
	if expired, ok := lost(); ok {
		at := ""
		if !expired.At.IsZero() {
			at = " at " + expired.At.UTC().Format("15:04:05") + " UTC"
		}
		return AnsweredQuestion{}, wrapped(dialog.ErrNoDialog, fmt.Sprintf("the question expired: Codex resolved it by "+
			"itself with no answer%s, about %s after asking it, and carried on without it. answer_session can no "+
			"longer reach it; send the answer with send_session, which it reads as an ordinary message",
			at, asks.CodexExpiry))
	}
	if screen, ok := dialog.ReadCodexScreen(plain); ok {
		return AnsweredQuestion{}, wrapped(dialog.ErrNotKeyAnswerable, "the child is on "+codexRefusal(screen))
	}
	return AnsweredQuestion{}, wrapped(dialog.ErrNoDialog, "the child is not on a Codex question this can answer: "+
		"no request_user_input dialog is on its screen and its rollout holds no question waiting. It may already "+
		"have been answered, or be resting at its own input line; use send_session to send it words instead")
}

func isSubmitWord(text string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(text), " ")) {
	case "submit", "submit answers", "proceed":
		return true
	}
	return false
}

func codexRefusal(screen dialog.Screen) string {
	switch screen.Kind {
	case dialog.ScreenPermission:
		return "a Codex approval prompt -- whether it may run that command, make those edits or call that tool " +
			"is a person's call: put it to your user word for word (read_session shows it), or ask the operator " +
			"to press the key on the board; send_session is held while a dialog stands"
	case dialog.ScreenElicitation:
		return "an input form an MCP server raised through Codex -- its answer goes to that server, and Codex keeps " +
			"no record of it to read back, so it is a person's to fill in at the pane: put it to your user word for word"
	case dialog.ScreenWorkspaceTrust:
		return "Codex's folder-trust prompt -- whether to work on contents nobody has vouched for is a person's to " +
			"answer, and the standing fix is the child's launch configuration rather than a keystroke"
	case dialog.ScreenUpdate:
		return "Codex's update prompt -- whether to update the CLI is the operator's call, at the pane"
	}
	return "a Codex dialog this does not answer; put it to your user word for word"
}
