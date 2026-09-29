package sessioncmd

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// Answering a several-question dialog.
//
// Claude Code asks several questions in one AskUserQuestion call as tabs, one
// question on the screen at a time (dialog.Stepper has the layout). A parent
// that had settled all four had to answer one, wait for the pane to move on,
// read it again and answer the next -- and nothing could answer the Submit
// page at the end, so the child went on waiting after its last question was
// answered. This fills them in one call: visit each question's tab, check the
// question on the screen is the one meant, pick or type the answer, check the
// tab ticked, and at the end check the review page holds what was sent and
// press Submit.
//
// Every keystroke is followed by a fresh capture before the next one. The
// arrows are counted from a cursor position, and a position read before the
// pane moved is exactly the stale reading that answers with the wrong choice.

// QuestionAnswer is one answer in a call answering several questions.
type QuestionAnswer struct {
	Question string `json:"question" jsonschema:"which question: its 1-based index, its header, or its text, as read_session's digest.questions lists them"`
	Answer   string `json:"answer" jsonschema:"the option's text to pick it, or your own words to type into that question's free-text row"`
}

// FilledAnswer is what one answer did.
type FilledAnswer struct {
	Index    int    `json:"index" jsonschema:"1-based question answered"`
	Header   string `json:"header,omitempty" jsonschema:"that question's header"`
	Answer   string `json:"answer" jsonschema:"the answer given"`
	Selected string `json:"selected,omitempty" jsonschema:"option it picked; empty when the answer was typed"`
	// question is the question's text as its tab drew it, which is what the
	// readback finds the answer under.
	question string
}

// want is the text the child should show for this answer: the option's
// label, or the words typed.
func (f FilledAnswer) want() string {
	if f.Selected != "" {
		return f.Selected
	}
	return f.Answer
}

// dialogPane is the three things answering needs from a pane, so the
// sequence can be driven against a real tmux pane or a model of one.
type dialogPane interface {
	// Capture is the pane with its escapes, which carry the active tab.
	Capture() (string, error)
	Keys(keys ...string) error
	// Type pastes words and presses Enter.
	Type(text string) error
}

type tmuxPane struct {
	driver *tmux.Driver
	id     string
}

func (p tmuxPane) Capture() (string, error)  { return p.driver.CapturePane(p.id) }
func (p tmuxPane) Keys(keys ...string) error { return p.driver.SendKeys(p.id, keys...) }
func (p tmuxPane) Type(text string) error    { return p.driver.SendText(p.id, text) }

// How long a pane is given to redraw after a keystroke, and how often it is
// looked at meanwhile. A test's model redraws at once.
var (
	settleTimeout = 3 * time.Second
	settlePoll    = 40 * time.Millisecond
)

// errDialogMoved is a pane that stopped showing what the sequence expected:
// somebody else answered, typed or cancelled while it ran.
var errDialogMoved = errors.New("the dialog changed under the answer")

// AnswerAll answers several questions of the dialog targetID is holding, and
// submits it when every question then has an answer and submit is set.
func (s *Sessions) AnswerAll(sessionID, targetID string, answers []QuestionAnswer, submit bool) (AnsweredQuestion, error) {
	if len(answers) == 0 {
		return AnsweredQuestion{}, errors.New("answers is empty; give one entry per question to answer")
	}
	for _, answer := range answers {
		if strings.TrimSpace(answer.Answer) == "" {
			return AnsweredQuestion{}, fmt.Errorf("the answer for question %q is empty", answer.Question)
		}
	}
	runtime, err := s.open()
	if err != nil {
		return AnsweredQuestion{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return AnsweredQuestion{}, err
	}
	target, err := runtime.child(caller, targetID)
	if err != nil {
		return AnsweredQuestion{}, err
	}
	pane := tmuxPane{runtime.driver, target.ID}
	raw, err := pane.Capture()
	if err != nil {
		return AnsweredQuestion{}, err
	}
	questions := dialog.Questions(raw, s.asked(target))
	if len(questions) == 0 {
		// The single-answer path has the words for every shape that is not a
		// question dialog; one answer there gets the same refusal.
		return runtime.answer(target, answers[0].Answer, "parent", caller.ID)
	}
	answered, err := fillDialog(pane, questions, answers, submit)
	answered.SessionID, answered.Name = target.ID, target.Name
	if err != nil {
		return answered, err
	}
	logging.Info("parent answered a child's dialog",
		"parent", caller.ID, "session", target.ID, "answers", len(answered.Answers), "submitted", answered.Submitted)
	return answered, nil
}

// asked is the pending AskUserQuestion call in target's transcript, or nil
// when there is none to read.
func (s *Sessions) asked(target store.Session) []convo.AskQuestion {
	if target.Tool != "claude" || target.AgentSessionID == "" {
		return nil
	}
	path := convo.TranscriptFor(s.claudeHome, target.AgentSessionID, target.Cwd)
	if path == "" {
		return nil
	}
	questions, _ := convo.PendingAsk(path)
	return questions
}

// planned is one answer resolved to the question it answers.
type planned struct {
	index  int
	answer string
}

// fillDialog is the sequence, over any pane. It validates every answer before
// the first keystroke, so a batch with one bad entry changes nothing.
func fillDialog(pane dialogPane, questions []dialog.Question, answers []QuestionAnswer, submit bool) (AnsweredQuestion, error) {
	result := AnsweredQuestion{Questions: questions}
	plan := make([]planned, 0, len(answers))
	seen := map[int]bool{}
	for _, answer := range answers {
		index, err := dialog.Resolve(questions, answer.Question)
		if err != nil {
			return result, err
		}
		if seen[index] {
			return result, fmt.Errorf("question %d is answered twice in answers", index+1)
		}
		seen[index] = true
		question := questions[index]
		if question.MultiSelect {
			return result, fmt.Errorf("question %d (%s) is a multi-select -- Enter ticks a box rather than "+
				"answering, so it is a person's to answer on the board; leave it out of answers and "+
				"answer the rest", index+1, question.Header)
		}
		plan = append(plan, planned{index, strings.TrimSpace(answer.Answer)})
	}
	slices.SortFunc(plan, func(a, b planned) int { return a.index - b.index })

	raw, err := pane.Capture()
	if err != nil {
		return result, err
	}
	_, tabbed := dialog.ParseStepper(raw)
	for _, step := range plan {
		if tabbed {
			if raw, err = navigateTo(pane, step.index); err != nil {
				return finish(pane, result, err)
			}
		}
		before := len(dialog.ParseAnswered(ansi.Strip(raw)))
		filled, err := answerOnScreen(pane, raw, questions[step.index], step.answer, tabbed)
		if err != nil {
			return finish(pane, result, err)
		}
		if !tabbed {
			if err := confirmEcho(pane, before, []FilledAnswer{filled}); err != nil {
				return finish(pane, result, err)
			}
			result.Answers = append(result.Answers, filled)
			result.Verified = true
			return finish(pane, result, nil)
		}
		// Recorded only once its tab ticks, so a batch that stops here does not
		// report an answer the dialog never took.
		if raw, err = waitFor(pane, func(raw string) bool {
			stepper, ok := dialog.ParseStepper(raw)
			return ok && step.index < len(stepper.Steps) && stepper.Steps[step.index].Answered
		}); err != nil {
			return finish(pane, result, fmt.Errorf("question %d was keyed but its tab never ticked: %w", step.index+1, err))
		}
		result.Answers = append(result.Answers, filled)
	}
	stepper, _ := dialog.ParseStepper(raw)
	switch {
	case submit && stepper.AllAnswered():
		if err := submitDialog(pane, len(stepper.Steps), result.Answers); err != nil {
			return finish(pane, result, err)
		}
		result.Submitted = true
	default:
		if _, err := checkReview(pane, len(stepper.Steps), result.Answers); err != nil {
			return finish(pane, result, err)
		}
		// Back to the first question still standing, where the dialog would
		// have gone by itself had the review page not been looked at.
		if next := slices.IndexFunc(stepper.Steps, func(s dialog.Step) bool { return !s.Answered }); next >= 0 {
			if _, err := navigateTo(pane, next); err != nil {
				return finish(pane, result, err)
			}
		}
	}
	result.Verified = true
	return finish(pane, result, nil)
}

// finish fills in what the dialog looks like now, so a caller whose batch
// stopped part way can see which answers landed.
func finish(pane dialogPane, result AnsweredQuestion, err error) (AnsweredQuestion, error) {
	if len(result.Answers) > 0 {
		last := result.Answers[len(result.Answers)-1]
		result.Answer, result.Selected = last.Answer, last.Selected
		result.Question = result.Questions[last.Index-1].Question
	}
	if raw, captureErr := pane.Capture(); captureErr == nil {
		if stepper, ok := dialog.ParseStepper(raw); ok && !result.Submitted {
			result.Standing = stepper.Unanswered()
			for i := range result.Questions {
				if i < len(stepper.Steps) {
					result.Questions[i].Answered = stepper.Steps[i].Answered
				}
			}
		}
	}
	for _, filled := range result.Answers {
		result.Questions[filled.Index-1].Answered = true
		result.Questions[filled.Index-1].Answer = filled.Answer
	}
	return result, err
}

// navigateTo moves the dialog to the tab at index, one arrow at a time, and
// returns the capture showing it. Right and Left move between tabs without
// touching an answer (measured on 2.1.283). They do nothing while the cursor
// is on a free-text row holding words, and that is reported rather than
// pressed through: somebody is typing an answer there.
func navigateTo(pane dialogPane, index int) (string, error) {
	for range 16 {
		raw, err := pane.Capture()
		if err != nil {
			return "", err
		}
		stepper, ok := dialog.ParseStepper(raw)
		if !ok {
			return "", fmt.Errorf("%w: its row of question tabs is gone -- it was answered or cancelled", errDialogMoved)
		}
		if stepper.Active < 0 {
			return "", errors.New("cannot tell which question the dialog is showing: the pane was captured without its colours")
		}
		if stepper.Active == index {
			return raw, nil
		}
		key := "Right"
		if index < stepper.Active {
			key = "Left"
		}
		from := stepper.Active
		if err := pane.Keys(key); err != nil {
			return "", err
		}
		if _, err := waitFor(pane, func(raw string) bool {
			moved, ok := dialog.ParseStepper(raw)
			return !ok || moved.Active != from
		}); err != nil {
			return "", fmt.Errorf("the dialog did not move off question %d; its free-text row may hold words "+
				"somebody is typing, so it was left alone", from+1)
		}
	}
	return "", fmt.Errorf("could not reach question %d", index+1)
}

// answerOnScreen keys one answer into the question the pane is showing.
func answerOnScreen(pane dialogPane, raw string, question dialog.Question, answer string, tabbed bool) (FilledAnswer, error) {
	filled := FilledAnswer{Index: question.Index, Header: question.Header, Answer: answer, question: question.Question}
	held, ok := dialog.Inspect(ansi.Strip(raw))
	if !ok {
		return filled, fmt.Errorf("%w: question %d is not on the screen", errDialogMoved, question.Index)
	}
	if held.Prompt != "" {
		filled.question = strings.Join(strings.Fields(held.Prompt), " ")
	}
	if question.Question != "" && tabbed && !strings.EqualFold(
		strings.Join(strings.Fields(held.Prompt), " "), strings.Join(strings.Fields(question.Question), " ")) {
		return filled, fmt.Errorf("%w: the tab for question %d shows %q, not %q",
			errDialogMoved, question.Index, held.Prompt, question.Question)
	}
	if refusal := held.Refusal(); refusal != "" {
		return filled, fmt.Errorf("question %d is %s", question.Index, refusal)
	}
	chosen := held.Choose(answer)
	if chosen > 0 && harnessEscape(held.Options[chosen-1]) {
		if strings.EqualFold(strings.TrimSpace(held.Options[chosen-1]), "chat about this") {
			return filled, fmt.Errorf("question %d: %q would leave the dialog for the conversation rather "+
				"than answer it; to talk it over, send the words as the answer and they are typed into "+
				"the question's free-text row", question.Index, held.Options[chosen-1])
		}
		chosen = 0
	}
	if chosen > 0 {
		filled.Selected = held.Options[chosen-1]
		keys := dialog.SelectKeys(held.Cursor, chosen)
		if keys == nil {
			return filled, fmt.Errorf("question %d is %s", question.Index, held.Refusal())
		}
		return filled, pane.Keys(keys...)
	}
	return filled, typeAnswer(pane, held, answer, question.Index)
}

// typeAnswer puts words into the dialog's free-text row and submits them.
// Words pasted with the cursor anywhere else are dropped by the dialog, and
// the Enter after them picks whichever option the cursor was on -- so the
// cursor is moved first and seen to arrive.
func typeAnswer(pane dialogPane, held dialog.Dialog, answer string, index int) error {
	if held.FreeText == 0 {
		return fmt.Errorf("question %d has no free-text row to type %q into; answer with one of its options", index, answer)
	}
	if held.Cursor == 0 {
		return fmt.Errorf("question %d is %s", index, held.Refusal())
	}
	if held.Cursor != held.FreeText {
		keys := dialog.SelectKeys(held.Cursor, held.FreeText)
		if err := pane.Keys(keys[:len(keys)-1]...); err != nil {
			return err
		}
		if _, err := waitFor(pane, func(raw string) bool {
			moved, ok := dialog.Inspect(ansi.Strip(raw))
			return ok && moved.Cursor == held.FreeText
		}); err != nil {
			return fmt.Errorf("%w: the cursor never reached question %d's free-text row", errDialogMoved, index)
		}
	}
	return pane.Type(answer)
}

func harnessEscape(option string) bool {
	switch strings.ToLower(strings.TrimSpace(option)) {
	case "type something", "type something.", "chat about this":
		return true
	}
	return false
}

// How long a child is given to print its record of an answer once the dialog
// has taken it.
var readbackTimeout = 10 * time.Second

// checkReview goes to the Submit tab and checks its review page shows every
// answer in filled under its own question, word for word. This is the
// readback for a several-question dialog: what the page lists is what Submit
// sends.
func checkReview(pane dialogPane, submitTab int, filled []FilledAnswer) (dialog.Review, error) {
	raw, err := navigateTo(pane, submitTab)
	if err != nil {
		return dialog.Review{}, err
	}
	review, ok := dialog.ParseReview(ansi.Strip(raw))
	if !ok {
		return review, fmt.Errorf("%w: the Submit tab is not showing its review page, so the answers "+
			"could not be read back", errDialogMoved)
	}
	for _, answer := range filled {
		got, found := registered(review.Answers, answer)
		if !found {
			return review, fmt.Errorf("question %d was answered %q, but the review page lists no answer "+
				"for it, so it cannot be confirmed and nothing was submitted", answer.Index, answer.want())
		}
		if !dialog.SameText(got, answer.want()) {
			return review, mismatch(answer, got, "nothing was submitted")
		}
	}
	return review, nil
}

// submitDialog checks the review page, presses Submit, and reads the
// answers back once more out of what the child prints on taking them.
func submitDialog(pane dialogPane, submitTab int, filled []FilledAnswer) error {
	review, err := checkReview(pane, submitTab, filled)
	if err != nil {
		return err
	}
	if !review.Complete {
		return errors.New("the review page says not every question is answered, so it was not submitted")
	}
	raw, err := pane.Capture()
	if err != nil {
		return err
	}
	before := len(dialog.ParseAnswered(ansi.Strip(raw)))
	keys := dialog.SelectKeys(review.Cursor, review.Submit)
	if keys == nil {
		return errors.New("cannot locate the cursor on the review page, so it was not submitted")
	}
	if err := pane.Keys(keys...); err != nil {
		return err
	}
	if _, err := waitFor(pane, func(raw string) bool {
		_, still := dialog.ParseStepper(raw)
		return !still
	}); err != nil {
		return errors.New("Submit was pressed but the dialog is still standing")
	}
	return confirmEcho(pane, before, filled)
}

// confirmEcho waits for the record Claude Code prints once it takes a
// dialog's answers -- a block more than the before the pane held -- and
// checks every answer in filled is in it as given.
func confirmEcho(pane dialogPane, before int, filled []FilledAnswer) error {
	deadline := time.Now().Add(readbackTimeout)
	var blocks [][]dialog.ReviewAnswer
	for {
		raw, err := pane.Capture()
		if err != nil {
			return err
		}
		if blocks = dialog.ParseAnswered(ansi.Strip(raw)); len(blocks) > before {
			break
		}
		if time.Now().After(deadline) {
			if len(filled) == 0 {
				return nil
			}
			return fmt.Errorf("the child never printed which answer it took, so %s cannot be confirmed; "+
				"read_session shows where it stands", describe(filled))
		}
		time.Sleep(settlePoll)
	}
	last := blocks[len(blocks)-1]
	for _, answer := range filled {
		got, found := registered(last, answer)
		if !found {
			return fmt.Errorf("the child's record of its answers does not list question %d, so %q cannot be "+
				"confirmed; read_session shows what it took", answer.Index, answer.want())
		}
		if !dialog.SameText(got, answer.want()) {
			return mismatch(answer, got, "the child has already taken it; send it a correction")
		}
	}
	return nil
}

// registered finds the answer listed for filled's question.
func registered(listed []dialog.ReviewAnswer, filled FilledAnswer) (string, bool) {
	for _, entry := range listed {
		if dialog.SameQuestion(entry.Question, filled.question) {
			return entry.Answer, true
		}
	}
	if filled.question == "" && len(listed) == 1 {
		return listed[0].Answer, true
	}
	return "", false
}

func mismatch(answer FilledAnswer, got, then string) error {
	return fmt.Errorf("%w: question %d was answered %q, but the child registered %q; %s",
		errWrongAnswer, answer.Index, answer.want(), dialog.Readable(got), then)
}

// errWrongAnswer is a readback showing the child took something other than
// what was answered.
var errWrongAnswer = errors.New("the answer did not land as given")

func describe(filled []FilledAnswer) string {
	parts := make([]string, 0, len(filled))
	for _, answer := range filled {
		parts = append(parts, fmt.Sprintf("question %d's answer %q", answer.Index, answer.want()))
	}
	return strings.Join(parts, " and ")
}

// waitFor captures the pane until done holds, and returns that capture.
func waitFor(pane dialogPane, done func(raw string) bool) (string, error) {
	deadline := time.Now().Add(settleTimeout)
	for {
		raw, err := pane.Capture()
		if err != nil {
			return "", err
		}
		if done(raw) {
			return raw, nil
		}
		if time.Now().After(deadline) {
			return raw, errors.New("timed out waiting for the pane to redraw")
		}
		time.Sleep(settlePoll)
	}
}
