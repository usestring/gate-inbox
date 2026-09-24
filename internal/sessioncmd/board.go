package sessioncmd

import (
	"errors"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
)

// The board's own reads and answers.
//
// Everything else in this package acts as a calling session, with that
// session's reach: its own children to answer, itself to leave alone. Code
// that runs beside the board rather than inside a session -- a watcher or
// scheduler, a view deciding what to badge -- is no session, and has
// the reach the operator has from the board: any agent session's pane, and
// its dialog. Only reads and answers are offered that way. Starting and
// ending sessions stay a session's acts, so that a spawn always has a parent,
// except for the helpers a board extension launches for itself: see
// BoardLaunch.
//
// An answer from here is still held to the dialog's own rules. A permission
// prompt or a first-run trust dialog is refused exactly as it is to a
// parent: the board lending its reach to code is not a person answering.

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
	pane = strings.TrimRight(ansi.Strip(pane), "\r\n")
	read = BoardPane{
		Session: runtime.sessionInfo(target, live, false),
		Text:    pane,
		Live:    live,
	}
	read.Session.Status = runtime.digest(target, pane, live, convo.Delta{}).Status
	if live {
		read.Dialog, read.HasDialog = dialog.Inspect(pane)
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
