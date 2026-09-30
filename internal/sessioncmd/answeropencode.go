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

type opencodeDriver struct{}

func init() { registerAnswerDriver("opencode", opencodeDriver{}) }

func (opencodeDriver) answer(r *runtime, s *Sessions, target store.Session, raw string, answers []QuestionAnswer,
	submit bool, guard *answerGuard, by, byID string) (AnsweredQuestion, error) {
	t := s.askTarget(target)
	call, pending := asks.Pending(t)
	record := func(id string) (asks.Result, bool) { return asks.ResultOf(t, id) }
	lost := func() (asks.Result, bool) { return asks.Unanswered(t) }
	answered, err := answerOpencode(tmuxPane{r.driver, target.ID}, call, pending, record, lost, raw, answers, submit, guard)
	answered.SessionID, answered.Name = target.ID, target.Name
	if pending {
		answered.readFrom = "OpenCode's session store"
		if !answered.Submitted {
			answered.readFrom = "its Submit page"
		}
	}
	if err != nil {
		logging.Warn(by+"'s answer to an OpenCode child's question did not land as given",
			by, byID, "session", target.ID, "err", err)
		return answered, fmt.Errorf("session %s: %w", target.ID, err)
	}
	logging.Info(by+" answered an OpenCode child's question", by, byID, "session", target.ID,
		"answers", len(answered.Answers), "submitted", answered.Submitted, "verified", answered.Verified)
	return answered, nil
}

type opencodeStep struct {
	index    int
	answer   string
	question dialog.Question
	options  []int
	custom   string
}

func (s opencodeStep) labels() []string {
	var out []string
	for _, n := range s.options {
		out = append(out, s.question.Options[n-1].Label)
	}
	if s.custom != "" {
		out = append(out, s.custom)
	}
	return out
}

func answerOpencode(pane dialogPane, call asks.Call, pending bool, record func(string) (asks.Result, bool),
	lost func() (asks.Result, bool), raw string, answers []QuestionAnswer, submit bool, guard *answerGuard) (AnsweredQuestion, error) {
	plain := ansi.Strip(raw)
	ask, ok := dialog.ParseOpencodeAsk(plain)
	if !ok {
		return opencodeNotAQuestion(plain, lost)
	}
	var asked []convo.AskQuestion
	if pending {
		asked = call.Questions
	}
	reading, _ := dialog.ReadQuestions("opencode", plain, asked)
	questions := reading.Questions
	result := AnsweredQuestion{Questions: questions}
	if ask.OnSubmit && len(answers) == 1 && strings.TrimSpace(answers[0].Question) == "" && isSubmitWord(answers[0].Answer) {
		return submitOpencode(pane, call, pending, record, questions, nil, result)
	}
	steps, err := planOpencode(questions, ask, asked, answers)
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
	tabbed := len(questions) > 1 || ask.Multi || len(ask.Tabs) > 0 || ask.Field > 0 || ask.OnSubmit
	if !tabbed {
		result, err = answerOpencodeSingle(pane, ask, steps[0], result)
		if err == nil && pending {
			err = readbackOpencode(call, record, steps, &result)
		}
		guard.finish(err)
		return result, err
	}
	result, err = fillOpencode(pane, asked, questions, steps, result)
	if err == nil {
		result, err = finishOpencode(pane, call, pending, record, asked, questions, steps, submit, result)
	}
	guard.finish(err)
	return result, err
}

func planOpencode(questions []dialog.Question, ask dialog.OpencodeAsk, asked []convo.AskQuestion,
	answers []QuestionAnswer) ([]opencodeStep, error) {
	var steps []opencodeStep
	seen := map[int]bool{}
	for _, answer := range answers {
		index := ask.OnScreen(asked)
		if strings.TrimSpace(answer.Question) != "" {
			var err error
			if index, err = dialog.Resolve(questions, answer.Question); err != nil {
				return nil, err
			}
		}
		if index < 0 || index >= len(questions) {
			return nil, errors.New("the dialog is on its Submit page, where there is no question on the screen; " +
				"name each question you answer, or answer \"Submit answers\" to send it")
		}
		if seen[index] {
			return nil, fmt.Errorf("question %d is answered twice in answers", index+1)
		}
		seen[index] = true
		text := strings.TrimSpace(answer.Answer)
		step := opencodeStep{index: index, answer: text, question: questions[index]}
		if n := step.question.Choose(text); n > 0 {
			step.options = []int{n}
		} else if step.question.MultiSelect {
			var rest []string
			for _, part := range splitChoices(text) {
				n := step.question.Choose(part)
				switch {
				case n > 0 && !slices.Contains(step.options, n):
					step.options = append(step.options, n)
				case n == 0:
					rest = append(rest, part)
				}
			}
			slices.Sort(step.options)
			step.custom = strings.Join(rest, ", ")
		} else {
			step.custom = text
		}
		steps = append(steps, step)
	}
	slices.SortFunc(steps, func(a, b opencodeStep) int { return a.index - b.index })
	return steps, nil
}

func splitChoices(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	var out []string
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}

func opencodeCustomRow(ask dialog.OpencodeAsk, question dialog.Question) int {
	if len(ask.Options) > len(question.Options) {
		return len(question.Options) + 1
	}
	for _, option := range ask.Options {
		if option.Label == dialog.OpencodeCustom {
			return option.Number
		}
	}
	return 0
}

func answerOpencodeSingle(pane dialogPane, ask dialog.OpencodeAsk, step opencodeStep, result AnsweredQuestion) (AnsweredQuestion, error) {
	filled := FilledAnswer{Index: step.index + 1, Header: step.question.Header, Answer: step.answer, question: step.question.Question}
	var err error
	if len(step.options) == 1 && step.custom == "" {
		filled.Selected = step.question.Options[step.options[0]-1].Label
		err = pane.Keys(strconv.Itoa(step.options[0]))
	} else {
		err = opencodeTypeCustom(pane, ask, step)
	}
	if err != nil {
		return result, err
	}
	if _, err := waitFor(pane, func(raw string) bool {
		_, still := dialog.ParseOpencodeAsk(ansi.Strip(raw))
		return !still
	}); err != nil {
		return result, fmt.Errorf("the answer was keyed but the question is still standing")
	}
	result.Answers = append(result.Answers, filled)
	result.Submitted = true
	result.Question, result.Answer, result.Selected = filled.question, filled.Answer, filled.Selected
	return result, nil
}

func opencodeTypeCustom(pane dialogPane, ask dialog.OpencodeAsk, step opencodeStep) error {
	row := opencodeCustomRow(ask, step.question)
	if row == 0 || row > 9 {
		return fmt.Errorf("question %d offers no row for words of your own; answer with one of its options", step.index+1)
	}
	if err := pane.Keys(strconv.Itoa(row)); err != nil {
		return err
	}
	if _, err := waitFor(pane, func(raw string) bool {
		now, ok := dialog.ParseOpencodeAsk(ansi.Strip(raw))
		return ok && now.Typing
	}); err != nil {
		return fmt.Errorf("%w: question %d's text field did not open", errDialogMoved, step.index+1)
	}
	return pane.Type(step.custom)
}

func opencodePosition(ask dialog.OpencodeAsk, asked []convo.AskQuestion, total int) int {
	if ask.OnSubmit {
		return total
	}
	return ask.OnScreen(asked)
}

func opencodeNavigate(pane dialogPane, asked []convo.AskQuestion, total, index int) (dialog.OpencodeAsk, error) {
	for range 2*total + 4 {
		raw, err := pane.Capture()
		if err != nil {
			return dialog.OpencodeAsk{}, err
		}
		ask, ok := dialog.ParseOpencodeAsk(ansi.Strip(raw))
		if !ok {
			return ask, fmt.Errorf("%w: the question dialog is gone -- it was answered or dismissed", errDialogMoved)
		}
		if ask.Typing {
			return ask, fmt.Errorf("%w: somebody is typing an answer into the dialog", errDialogMoved)
		}
		at := opencodePosition(ask, asked, total)
		if at < 0 {
			return ask, errors.New("cannot tell which question the dialog is showing, so nothing was keyed")
		}
		if at == index {
			return ask, nil
		}
		key := "Tab"
		if index < at {
			key = "BTab"
		}
		if err := pane.Keys(key); err != nil {
			return ask, err
		}
		if _, err := waitFor(pane, func(raw string) bool {
			moved, ok := dialog.ParseOpencodeAsk(ansi.Strip(raw))
			return !ok || opencodePosition(moved, asked, total) != at
		}); err != nil {
			return ask, fmt.Errorf("the dialog did not move off question %d", at+1)
		}
	}
	return dialog.OpencodeAsk{}, fmt.Errorf("could not reach question %d", index+1)
}

func fillOpencode(pane dialogPane, asked []convo.AskQuestion, questions []dialog.Question, steps []opencodeStep,
	result AnsweredQuestion) (AnsweredQuestion, error) {
	total := len(questions)
	for _, step := range steps {
		ask, err := opencodeNavigate(pane, asked, total, step.index)
		if err != nil {
			return result, err
		}
		if step.question.Question != "" && !dialog.SameText(ask.Prompt, step.question.Question) {
			return result, fmt.Errorf("%w: question %d on the screen reads %q, not %q", errDialogMoved, step.index+1,
				ask.Prompt, step.question.Question)
		}
		filled := FilledAnswer{Index: step.index + 1, Header: step.question.Header, Answer: step.answer, question: step.question.Question}
		if !ask.Multi {
			if len(step.options) == 1 {
				filled.Selected = step.question.Options[step.options[0]-1].Label
				err = pane.Keys(strconv.Itoa(step.options[0]))
			} else {
				err = opencodeTypeCustom(pane, ask, step)
			}
			if err != nil {
				return result, err
			}
			if _, err := waitFor(pane, func(raw string) bool {
				moved, ok := dialog.ParseOpencodeAsk(ansi.Strip(raw))
				return !ok || !moved.Typing && opencodePosition(moved, asked, total) != step.index
			}); err != nil {
				return result, fmt.Errorf("question %d was keyed but the dialog did not move on", step.index+1)
			}
			result.Answers = append(result.Answers, filled)
			continue
		}
		if err := tickOpencode(pane, ask, step); err != nil {
			return result, err
		}
		result.Answers = append(result.Answers, filled)
	}
	return result, nil
}

func tickOpencode(pane dialogPane, ask dialog.OpencodeAsk, step opencodeStep) error {
	custom := opencodeCustomRow(ask, step.question)
	for _, option := range ask.Options {
		if option.Number == custom {
			continue
		}
		want := slices.Contains(step.options, option.Number)
		if want == option.Checked {
			continue
		}
		if option.Number > 9 {
			return fmt.Errorf("question %d's option %d is past the number keys", step.index+1, option.Number)
		}
		if err := pane.Keys(strconv.Itoa(option.Number)); err != nil {
			return err
		}
		n := option.Number
		if _, err := waitFor(pane, func(raw string) bool {
			now, ok := dialog.ParseOpencodeAsk(ansi.Strip(raw))
			return ok && n <= len(now.Options) && now.Options[n-1].Checked == want
		}); err != nil {
			return fmt.Errorf("%w: option %d of question %d did not change", errDialogMoved, n, step.index+1)
		}
	}
	if step.custom == "" {
		return nil
	}
	if err := opencodeTypeCustom(pane, ask, step); err != nil {
		return err
	}
	if _, err := waitFor(pane, func(raw string) bool {
		now, ok := dialog.ParseOpencodeAsk(ansi.Strip(raw))
		return ok && !now.Typing
	}); err != nil {
		return fmt.Errorf("%w: question %d's words were typed but its text field is still open", errDialogMoved, step.index+1)
	}
	return nil
}

func finishOpencode(pane dialogPane, call asks.Call, pending bool, record func(string) (asks.Result, bool),
	asked []convo.AskQuestion, questions []dialog.Question, steps []opencodeStep, submit bool,
	result AnsweredQuestion) (AnsweredQuestion, error) {
	total := len(questions)
	ask, err := opencodeNavigate(pane, asked, total, total)
	if err != nil {
		return result, err
	}
	if err := checkOpencodeReview(ask, questions, steps); err != nil {
		return result, err
	}
	open := 0
	first := -1
	for i, row := range ask.Review {
		if row.Answer == dialog.OpencodeNotAnswered {
			open++
			if first < 0 {
				first = i
			}
		}
	}
	markOpencode(&result, ask)
	if submit && open == 0 {
		return submitOpencode(pane, call, pending, record, questions, steps, result)
	}
	result.Standing = open
	result.Verified = true
	if first >= 0 {
		if _, err := opencodeNavigate(pane, asked, total, first); err != nil {
			return result, err
		}
	}
	return result, nil
}

func markOpencode(result *AnsweredQuestion, ask dialog.OpencodeAsk) {
	for i := range result.Questions {
		for _, row := range ask.Review {
			if strings.EqualFold(row.Question, result.Questions[i].Header) && row.Answer != dialog.OpencodeNotAnswered {
				result.Questions[i].Answered, result.Questions[i].Answer = true, row.Answer
			}
		}
	}
	if last := len(result.Answers) - 1; last >= 0 {
		result.Question, result.Answer, result.Selected = result.Answers[last].question, result.Answers[last].Answer,
			result.Answers[last].Selected
	}
}

func checkOpencodeReview(ask dialog.OpencodeAsk, questions []dialog.Question, steps []opencodeStep) error {
	if !ask.OnSubmit {
		return fmt.Errorf("%w: the Submit tab is not showing its review, so the answers could not be read back", errDialogMoved)
	}
	for _, step := range steps {
		header := questions[step.index].Header
		want := strings.Join(step.labels(), ", ")
		got, found := "", false
		for _, row := range ask.Review {
			if strings.EqualFold(row.Question, header) {
				got, found = row.Answer, true
			}
		}
		filled := FilledAnswer{Index: step.index + 1, Answer: want}
		if !found {
			return fmt.Errorf("question %d was answered %q, but the review page lists no answer for it, so "+
				"nothing was submitted", step.index+1, want)
		}
		if !dialog.SameText(got, want) {
			return mismatch(filled, got, "nothing was submitted")
		}
	}
	return nil
}

func submitOpencode(pane dialogPane, call asks.Call, pending bool, record func(string) (asks.Result, bool),
	questions []dialog.Question, steps []opencodeStep, result AnsweredQuestion) (AnsweredQuestion, error) {
	if err := pane.Keys("Enter"); err != nil {
		return result, err
	}
	if _, err := waitFor(pane, func(raw string) bool {
		_, still := dialog.ParseOpencodeAsk(ansi.Strip(raw))
		return !still
	}); err != nil {
		return result, errors.New("Submit was pressed but the dialog is still standing")
	}
	result.Submitted, result.Standing = true, 0
	if !pending {
		return result, nil
	}
	if steps == nil {
		for i := range questions {
			steps = append(steps, opencodeStep{index: i, question: questions[i]})
		}
		got, ok := asks.Result{}, false
		deadline := time.Now().Add(readbackTimeout)
		for !ok || got.Outcome == asks.Unresolved {
			if time.Now().After(deadline) {
				return result, errors.New("Submit was pressed but OpenCode has not stored the answers yet")
			}
			time.Sleep(settlePoll)
			got, ok = record(call.ID)
		}
		if got.Outcome != asks.Answered {
			return result, fmt.Errorf("%w: OpenCode stored the question as %s", errWrongAnswer, got.Outcome)
		}
		result.Verified = true
		return result, nil
	}
	return result, readbackOpencode(call, record, steps, &result)
}

func readbackOpencode(call asks.Call, record func(string) (asks.Result, bool), steps []opencodeStep, result *AnsweredQuestion) error {
	deadline := time.Now().Add(readbackTimeout)
	var got asks.Result
	for {
		var ok bool
		got, ok = record(call.ID)
		if ok && got.Outcome != asks.Unresolved {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("OpenCode has not stored the answers to its question yet, so they cannot be confirmed; " +
				"read_session shows where it stands")
		}
		time.Sleep(settlePoll)
	}
	if got.Outcome != asks.Answered {
		return fmt.Errorf("%w: OpenCode stored the question as %s rather than answered", errWrongAnswer, got.Outcome)
	}
	for _, step := range steps {
		if step.index >= len(got.Answers) {
			return fmt.Errorf("OpenCode's record of the question has no answer for question %d", step.index+1)
		}
		want, have := step.labels(), got.Answers[step.index].Labels
		if !sameLabels(want, have) {
			return mismatch(FilledAnswer{Index: step.index + 1, Answer: strings.Join(want, ", ")},
				strings.Join(have, ", "), "the child has already taken it; send it a correction")
		}
	}
	result.Verified = true
	return nil
}

func sameLabels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, x := range a {
		found := false
		for i, y := range b {
			if !used[i] && dialog.SameText(x, y) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func opencodeNotAQuestion(plain string, lost func() (asks.Result, bool)) (AnsweredQuestion, error) {
	if _, ok := dialog.ReadOpencodeScreen(plain); ok {
		return AnsweredQuestion{}, wrapped(dialog.ErrNotKeyAnswerable, "the child is on an OpenCode permission ask -- "+
			"whether it may run that command, make that edit, fetch that URL or reach outside its directory is a "+
			"person's call: put it to your user word for word (read_session shows it), or ask the operator to pick "+
			"Allow once, Always allow or Reject on the board; send_session is held while a dialog stands")
	}
	if gone, ok := lost(); ok && gone.Outcome == asks.Dismissed {
		return AnsweredQuestion{}, wrapped(dialog.ErrNoDialog, "the child's question was dismissed at its pane without "+
			"an answer, and it carried on without it; answer_session can no longer reach it, so send the answer "+
			"with send_session if it still matters")
	}
	return AnsweredQuestion{}, wrapped(dialog.ErrNoDialog, "the child is not on an OpenCode question this can answer: no "+
		"question dialog is on its screen. It may already have been answered, or be resting at its own input line; "+
		"use send_session to send it words instead")
}
