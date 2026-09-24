package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
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
	pane, err := r.driver.CapturePane(target.ID)
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
	held, ok := dialog.Inspect(ansi.Strip(pane))
	if !ok {
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
	chosen := held.Choose(reply)
	keys, err := dialog.AnswerKeys(held, reply)
	if errors.Is(err, dialog.ErrNotKeyAnswerable) {
		// Named rather than described. Two of these four want a person on the
		// board and two want this caller to look again in a moment, and one
		// sentence covering all of them told a manager neither.
		return AnsweredQuestion{}, wrapped(err, fmt.Sprintf(
			"session %s is on %s", target.ID, held.Refusal()))
	}
	if err != nil {
		return AnsweredQuestion{}, err
	}
	answered := AnsweredQuestion{
		SessionID: target.ID,
		Name:      target.Name,
		Question:  held.Question(),
		Answer:    reply,
	}
	if standing := held.Standing(); standing > 1 {
		answered.Standing = standing - 1
	}
	// len(keys) > 0 is exactly the case where an option was named: selectKeys
	// sends nothing for a want of 0, which is Choose's answer for words that
	// are nobody's option.
	if len(keys) > 0 {
		answered.Selected = held.Options[chosen-1]
		logging.Info(by+" answered a child's question by selection",
			by, byID, "session", target.ID, "option", answered.Selected)
		if err := r.driver.SendKeys(target.ID, keys...); err != nil {
			return AnsweredQuestion{}, err
		}
		return answered, nil
	}
	logging.Info(by+" answered a child's question by typing",
		by, byID, "session", target.ID)
	if err := r.driver.SendText(target.ID, reply); err != nil {
		return AnsweredQuestion{}, err
	}
	return answered, nil
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
