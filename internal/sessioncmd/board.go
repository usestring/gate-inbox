package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// The board's own reads and answers.
//
// Everything else in this package acts as a calling session, with that
// session's reach: its own children to answer, itself to leave alone. Code
// that runs beside the board rather than inside a session -- a watcher or
// scheduler, a view deciding what to badge -- is no session, and has
// the reach the operator has from the board: any agent session's pane, and
// its dialog. Reads and answers are offered that way. Starting and ending
// sessions stay a session's acts, so that a spawn always has a parent, except
// for what a board extension does for itself: launching its helpers
// (BoardLaunch), messaging and ending the sessions it watches (BoardSend,
// BoardKill, which ends their terminals too), and starting one over in its
// own seat (BoardReplace), each held to the checks a session's own tool is
// held to.
//
// An answer from here is still held to the dialog's own rules. A permission
// prompt or a first-run trust dialog is refused exactly as it is to a
// parent: the board lending its reach to code is not a person answering.

// nestedUnderAgent allows a terminal whose parent is an agent session still
// on the board: one its parent could close, and ended with its parent.
func (r *runtime) nestedUnderAgent(terminal store.Session) error {
	refused := fmt.Errorf("terminal %s is nested under no agent session, so it is not a session's to end", terminal.ID)
	if terminal.ParentID == "" {
		return refused
	}
	parent, err := r.store.Get(terminal.ParentID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return refused
	case err != nil:
		return err
	case r.cfg.Tools[parent.Tool].Shell:
		return refused
	}
	return nil
}

// errBoardHasNoSelf refuses SelfParent, which names the caller, from the
// board, which is no session.
var errBoardHasNoSelf = errors.New(`the board is no session, so "me" names nobody; pass a session id as the parent`)

// BoardPane is one session's screen, read on the board's behalf.
type BoardPane struct {
	// Session is the row, with Status read off the pane when it is live.
	Session Session
	// Text is the visible pane with its escapes stripped, or the screen the
	// session was last captured on when it is not running.
	Text string
	// Live is false when Text is that stored screen.
	Live bool
	// Dialog is the dialog the live pane is holding, guarded kinds included,
	// and HasDialog whether there is one. A stored screen holds none: the
	// question on it can no longer be answered.
	Dialog    dialog.Dialog
	HasDialog bool
	// AtPrompt is a live session resting at its prompt with nothing
	// written there and nobody typing: one BoardCommand would type into.
	AtPrompt bool
}

// BoardGet is one agent session, as Get reports it, without a caller.
func (s *Sessions) BoardGet(targetID string) (got Session, err error) {
	defer start("sessioncmd.board.get", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	return runtime.sessionInfo(target, runtime.driver.Exists(target.ID), false), nil
}

// BoardList is the agent sessions opts keeps, as List reports them, without a
// caller.
func (s *Sessions) BoardList(opts ListOptions) (list SessionList, err error) {
	defer start("sessioncmd.board.list").done(&err)
	if strings.EqualFold(strings.TrimSpace(opts.Parent), SelfParent) {
		return SessionList{}, errBoardHasNoSelf
	}
	runtime, err := s.open()
	if err != nil {
		return SessionList{}, err
	}
	defer runtime.store.Close()
	return runtime.list("", opts)
}

// BoardRead is what an agent session's pane shows, and the dialog it is
// holding.
func (s *Sessions) BoardRead(targetID string) (read BoardPane, err error) {
	defer start("sessioncmd.board.read", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return BoardPane{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return BoardPane{}, err
	}
	live := runtime.driver.Exists(target.ID)
	var pane string
	if live {
		pane, err = runtime.driver.CapturePane(target.ID)
	} else {
		pane, err = runtime.store.Snapshot(target.ID)
	}
	if err != nil {
		return BoardPane{}, err
	}
	clean := ansi.Strip(pane)
	pane = strings.TrimRight(clean, "\r\n")
	read = BoardPane{
		Session: runtime.sessionInfo(target, live, false),
		Text:    pane,
		Live:    live,
	}
	read.Session.Status = runtime.digest(target, pane, live, convo.Delta{}).Status
	if live {
		read.Dialog, read.HasDialog = dialog.Inspect(pane)
		engine, err := status.NewEngine(runtime.cfg)
		if err != nil {
			return BoardPane{}, err
		}
		read.AtPrompt = runtime.promptHold(engine, target, clean) == nil
	}
	return read, nil
}

// BoardAnswer picks or types reply into the dialog an agent session is
// holding, as Answer does for a parent, on behalf of the board.
func (s *Sessions) BoardAnswer(targetID, reply string) (answered AnsweredQuestion, err error) {
	defer start("sessioncmd.board.answer", sessionAttr(targetID)).done(&err)
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return AnsweredQuestion{}, errEmptyAnswer
	}
	runtime, err := s.open()
	if err != nil {
		return AnsweredQuestion{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return AnsweredQuestion{}, err
	}
	return runtime.answer(target, reply, "board", "")
}

// BoardSend queues message for an agent session on behalf of the board
// extension extensionID, as Send does for a session: the same checks, but
// the larger limit maxBoardMessageBytes on the message, no terminals, nothing archived or not running, and no interrupt without a
// way to stop the turn. The message is queued under the extension's own
// sender, so its rate and dedupe budget is its own, and it is delivered
// fenced as the extension's rather than as a person's or another agent's.
func (s *Sessions) BoardSend(extensionID, targetID, message, subject string, interrupt bool) (SendResult, error) {
	return s.boardSend(extensionID, targetID, message, subject, false, interrupt)
}

// BoardSendAsOperator queues message as BoardSend does, but delivered as the
// operator's own words, with no fence: the text SendAsHuman queues. Nothing
// checks that the operator asked for it, so only a trusted, compiled-in
// extension reaches it. It is queued under a sender of the extension's own,
// store.OperatorVoicedSenderID, so its subject supersedes only this
// extension's earlier operator-voiced messages, never the operator's.
func (s *Sessions) BoardSendAsOperator(extensionID, targetID, message, subject string, interrupt bool) (SendResult, error) {
	return s.boardSend(extensionID, targetID, message, subject, true, interrupt)
}

func (s *Sessions) boardSend(extensionID, targetID, message, subject string, asOperator, interrupt bool) (result SendResult, err error) {
	op := start("sessioncmd.board.send", sessionAttr(targetID))
	defer func() { op.done(&err, tracing.Attr{Key: "as_operator", Value: asOperator}) }()
	if extensionID == "" {
		return SendResult{}, errors.New("a board message needs the extension sending it")
	}
	message, subject, err = checkMessage(message, subject, maxBoardMessageBytes)
	if err != nil {
		return SendResult{}, err
	}
	runtime, err := s.open()
	if err != nil {
		return SendResult{}, err
	}
	defer runtime.store.Close()
	from := sender{id: store.ExtensionSenderID(extensionID), name: extensionID}
	if asOperator {
		from.id = store.OperatorVoicedSenderID(extensionID)
	}
	return runtime.enqueue(from, targetID, message, subject, interrupt)
}

// BoardWithdraw drops what the board extension extensionID still has
// queued for an agent session: all of it, or with a subject only the
// messages on it. Only the extension's own sender is touched, so it can
// never take back what a session or the operator sent.
func (s *Sessions) BoardWithdraw(extensionID, targetID, subject string) (dropped int, err error) {
	defer start("sessioncmd.board.withdraw", sessionAttr(targetID)).done(&err)
	if extensionID == "" {
		return 0, errors.New("withdrawing needs the extension that sent the messages")
	}
	runtime, err := s.open()
	if err != nil {
		return 0, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return 0, err
	}
	return runtime.store.Withdraw(target.ID, store.ExtensionSenderID(extensionID), strings.TrimSpace(subject), time.Now())
}

// BoardKill ends an agent session's pane and leaves its row dead, as Kill
// does for a session; the board has no self to spare. It ends a terminal the
// same way when some session could close it -- one nested under an agent --
// so the board reaches every terminal a session does and none besides: the
// operator's own shells, nested under nobody, are left alone.
// The build's kill observers hear of an agent session's kill as the
// board's; a terminal's end is not reported.
func (s *Sessions) BoardKill(targetID string) (killed Session, err error) {
	defer start("sessioncmd.board.kill", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.killable(targetID, runtime.nestedUnderAgent)
	if err != nil {
		return Session{}, err
	}
	if err := s.endSession(runtime, target, store.EndKilled); err != nil {
		return Session{}, err
	}
	target.Status = status.Dead
	if !runtime.cfg.Tools[target.Tool].Shell {
		sessionhooks.Killed(target, extension.KillByBoard, "")
	}
	return runtime.sessionInfo(target, false, false), nil
}

// BoardArchive files one of the board extension extensionID's own helpers
// away, ending it first if it is running, as Archive does for a session. A
// helper is a session the extension launched with a role of its own; every
// other session is somebody else's to archive, and is refused.
func (s *Sessions) BoardArchive(extensionID, targetID string) (filed Session, err error) {
	defer start("sessioncmd.board.archive", sessionAttr(targetID)).done(&err)
	if extensionID == "" {
		return Session{}, errors.New("a board archive needs the extension archiving")
	}
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	if !strings.HasPrefix(target.Role, extensionID+"/") {
		return Session{}, fmt.Errorf("session %s was not launched by extension %q for a role of its own, so it is not the extension's to archive", target.ID, extensionID)
	}
	return s.file(runtime, target, true)
}
