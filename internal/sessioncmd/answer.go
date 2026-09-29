package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// Answering a child's question.
//
// A session that fans out owns what it spawned, and a child stopped on a
// question is the one moment that ownership means anything: until somebody
// answers, the work the parent asked for is not happening. Without this a
// fan-out leaves every question standing in front of a person who did not ask
// it -- and the parent, which knows what it wanted and why, cannot reach the
// screen at all.
//
// send_session is not that path and must not become it: a message is queued
// until the recipient is at rest precisely so it never lands on a prompt, and
// a dialog is a prompt. This is the other half -- keys into a pane that is
// holding a question -- and it is confined to the one relationship that
// justifies it. A parent answers its own children. Nobody answers anybody
// else's, and no session answers a stranger.

// AnsweredQuestion is what an answer did to the child's dialog.
type AnsweredQuestion struct {
	SessionID string `json:"session_id" jsonschema:"child session that was holding the question"`
	Name      string `json:"name" jsonschema:"that session's name"`
	Question  string `json:"question" jsonschema:"the question as its pane showed it"`
	Answer    string `json:"answer" jsonschema:"what was sent back"`
	// Selected is the option the answer landed on, or "" for an answer typed
	// in the dialog's own text field.
	Selected string `json:"selected,omitempty" jsonschema:"option the answer picked; empty when the answer was typed instead"`
	Standing int    `json:"standing_questions,omitempty" jsonschema:"questions still unanswered in the same dialog after this one"`
	// Answers is each answer a call filled in, for a dialog asking several
	// questions; Question, Answer and Selected above repeat the last of them.
	Answers []FilledAnswer `json:"answers,omitempty" jsonschema:"each question this call answered, in order"`
	// Submitted is a several-question dialog sent: every question had an
	// answer, the review page showed them, and Submit was pressed.
	Submitted bool `json:"submitted,omitempty" jsonschema:"true when the dialog was submitted and the child has its answers; false with standing_questions above zero means it is still waiting on the rest"`
	// Verified is the child's screen read back after the answer and found to
	// hold exactly the answer given: the Submit page or the answered-questions
	// record Claude Code prints. An answer that reads back as anything else is
	// an error, never a result.
	Verified bool `json:"verified" jsonschema:"true when the child's screen was read back after answering and shows exactly the answers given; false only for a Codex dialog, which draws no record to read"`
	// Questions is every question of the dialog as it stands after the call.
	Questions []dialog.Question `json:"questions,omitempty" jsonschema:"every question of the dialog after this call, with which are answered"`
}

// Answer picks or types reply into the question dialog targetID is holding.
//
// Only the session that spawned the child may call it. That is read off
// spawned_by rather than off parent_id: the tree carries one level, so a
// grandchild is filed against the caller's own root, and taking ownership
// from that placement refused every spawner its own children's dialogs while
// putting the root in front of questions it never assigned.
//
// One call answers one question. A dialog asking several reports the rest as
// Standing and is answered by calling again; AnswerKeys holds why that is not
// a list.
func (s *Sessions) Answer(sessionID, targetID, reply string) (AnsweredQuestion, error) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return AnsweredQuestion{}, errEmptyAnswer
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
	return runtime.answer(target, reply, "parent", caller.ID)
}

var errEmptyAnswer = errors.New(
	"answer is empty; give the text of the option to pick, or the words to type instead")

// answer keys or types reply into the dialog target's pane is holding, once
// the caller has settled who may. by and byID name who answered, in the log.
func (r *runtime) answer(target store.Session, reply, by, byID string) (AnsweredQuestion, error) {
	pane := tmuxPane{r.driver, target.ID}
	raw, err := pane.Capture()
	if err != nil {
		return AnsweredQuestion{}, err
	}
	// dialog.Parse anchors its legend at line start and Claude Code draws that
	// legend dimmed, so an unstripped capture -- CapturePane keeps the escapes
	// (-e) for previews -- reads as no dialog at all. This path captures its
	// own, so it strips it here.
	// dialog.Inspect rather than dialog.Parse: the guarded kinds are wanted here
	// precisely because they have to be refused, and a refusal that cannot say
	// which shape it saw is the defect this path had. See the two messages
	// below -- they send the caller to two different places.
	plain := ansi.Strip(raw)
	held, ok := dialog.Inspect(plain)
	if !ok {
		if _, onReview := dialog.ParseReview(plain); onReview {
			return r.submitReview(target, raw, reply, by, byID)
		}
		if dialog.Standing(plain) {
			return AnsweredQuestion{}, wrapped(dialog.ErrNotKeyAnswerable, fmt.Sprintf(
				"session %s is on a dialog this cannot read, so no keystroke is sent to it; what it "+
					"asks is a person's call: put it to your user word for word (read_session shows it), "+
					"or ask the operator to answer it on the board. %s is held while a dialog stands",
				target.ID, r.words.Send))
		}
		// Not an error about the answer: there is no dialog on the screen. The
		// question was answered by somebody who got there first, or the child
		// is resting at its own input line having ended a turn on a question in
		// prose -- which takes words, not keystrokes. Saying which shapes are
		// read at all is what stops a caller retrying this one forever.
		return AnsweredQuestion{}, wrapped(dialog.ErrNoDialog, fmt.Sprintf(
			"session %s is not on a dialog this can read: no numbered options under a legend it "+
				"knows (Claude Code's AskUserQuestion or permission prompt; Codex's command "+
				"approval, first-run trust or request_user_input). It may already have been "+
				"answered, or be resting at its own input line. Use %s to send it words instead",
			target.ID, r.words.Send))
	}
	if _, err := dialog.AnswerKeys(held, reply); errors.Is(err, dialog.ErrNotKeyAnswerable) {
		// Named rather than described. Two of these four want a person on the
		// board and two want this caller to look again in a moment, and one
		// sentence covering all of them told a manager neither.
		return AnsweredQuestion{}, wrapped(err, fmt.Sprintf(
			"session %s is on %s", target.ID, held.Refusal()))
	}
	answered, err := answerHeld(pane, raw, held, reply)
	answered.SessionID, answered.Name = target.ID, target.Name
	if err != nil {
		logging.Warn(by+"'s answer to a child's question did not land as given",
			by, byID, "session", target.ID, "err", err)
		return AnsweredQuestion{}, fmt.Errorf("session %s: %w", target.ID, err)
	}
	logging.Info(by+" answered a child's question",
		by, byID, "session", target.ID, "option", answered.Selected, "verified", answered.Verified)
	return answered, nil
}

// answerHeld answers the question held is, on pane, and reads back what the
// child took.
func answerHeld(pane dialogPane, raw string, held dialog.Dialog, reply string) (AnsweredQuestion, error) {
	answered := AnsweredQuestion{Question: held.Question(), Answer: reply}
	switch {
	case held.Kind == dialog.KindAsk && held.Steps > 0:
		questions := dialog.Questions(raw, nil)
		on := slices.IndexFunc(questions, func(q dialog.Question) bool { return q.OnScreen })
		if on < 0 {
			return answered, errors.New("cannot tell which of the dialog's questions is on the screen, so nothing was keyed")
		}
		filled, err := fillDialog(pane, questions, []QuestionAnswer{{Question: strconv.Itoa(on + 1), Answer: reply}}, true)
		filled.Question = answered.Question
		return filled, err
	case held.Kind == dialog.KindAsk:
		before := len(dialog.ParseAnswered(ansi.Strip(raw)))
		question := dialog.Question{Index: 1, Question: strings.Join(strings.Fields(held.Prompt), " ")}
		filled, err := answerOnScreen(pane, raw, question, reply, false)
		answered.Selected = filled.Selected
		if err != nil {
			return answered, err
		}
		if err := confirmEcho(pane, before, []FilledAnswer{filled}); err != nil {
			return answered, err
		}
		answered.Verified = true
		return answered, nil
	}
	// Codex's request_user_input draws no record of the answer it took, so
	// there is nothing on its screen to read back.
	keys, _ := dialog.AnswerKeys(held, reply)
	if len(keys) > 0 {
		answered.Selected = held.Options[held.Choose(reply)-1]
		return answered, pane.Keys(keys...)
	}
	return answered, pane.Type(reply)
}

// submitReview answers the Submit page a several-question dialog ends on:
// "Submit answers" sends it, and anything else is refused, because the page
// holds no question to put words to.
func (r *runtime) submitReview(target store.Session, pane, reply, by, byID string) (AnsweredQuestion, error) {
	switch strings.ToLower(strings.Join(strings.Fields(reply), " ")) {
	case "submit", "submit answers":
	default:
		return AnsweredQuestion{}, fmt.Errorf("session %s is on the Submit page of a dialog asking several "+
			"questions, where there is nothing to answer; answer \"Submit answers\" to send it, or pass "+
			"answers naming a question to change one first", target.ID)
	}
	stepper, _ := dialog.ParseStepper(pane)
	if err := submitDialog(tmuxPane{r.driver, target.ID}, len(stepper.Steps), nil); err != nil {
		return AnsweredQuestion{}, err
	}
	logging.Info(by+" submitted a child's dialog", by, byID, "session", target.ID)
	return AnsweredQuestion{
		SessionID: target.ID,
		Name:      target.Name,
		Question:  "Review your answers",
		Answer:    reply,
		Selected:  "Submit answers",
		Submitted: true,
	}, nil
}

// wrapped is err under a message of its own, so the words a caller reads
// stay the ones written for it and errors.Is still finds the sentinel.
func wrapped(err error, message string) error { return messageError{message, err} }

type messageError struct {
	message string
	err     error
}

func (e messageError) Error() string { return e.message }
func (e messageError) Unwrap() error { return e.err }

// child resolves a session the caller is entitled to act on the screen of:
// one it spawned, at whatever depth the board files it. The refusal names who
// does own the row, because an agent reading it can pass the question on.
func (r *runtime) child(caller store.Session, targetID string) (store.Session, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return store.Session{}, fmt.Errorf("session_id is empty; call %s to get one", r.words.ListSessions)
	}
	if targetID == caller.ID {
		return store.Session{}, errors.New(
			"that is this session; a session cannot answer its own dialog through the manager")
	}
	target, err := r.store.Get(targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, fmt.Errorf(
			"session %s does not exist; call %s for current ids", targetID, r.words.ListSessions)
	}
	if err != nil {
		return store.Session{}, err
	}
	if owner := store.SpawnerOf(target); owner != caller.ID {
		if owner == "" {
			return store.Session{}, fmt.Errorf(
				"session %s is nobody's child, so no session owns its screen; a person answers it "+
					"on the board", target.ID)
		}
		return store.Session{}, fmt.Errorf(
			"session %s was spawned by session %s, not by this one; only the session that spawned "+
				"a child answers it", target.ID, owner)
	}
	return target, nil
}
