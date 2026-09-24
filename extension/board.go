package extension

import (
	"context"

	"github.com/usestring/gate-inbox/internal/dialog"
)

// Board is the board as code that runs beside it sees it, rather than as
// one session: code watching sessions from outside them, a view deciding what to
// badge. Host acts as the session its MCP server serves, with that
// session's reach; Board has the operator's reach from the board, over
// reads and answers only. Starting and ending sessions stay a session's
// acts, through Host.
//
// An answer through Board is held to the dialog's own rules: a guarded
// dialog is refused here as it is to a session.
type Board interface {
	// ConfigDir is the operator's config directory.
	ConfigDir() string
	// Get is one agent session, without reading its pane.
	Get(ctx context.Context, id string) (SessionInfo, error)
	// List is the agent sessions filter keeps, in the order the board
	// lists them.
	List(ctx context.Context, filter SessionFilter) (SessionList, error)
	// ReadPane is what a session's pane is showing and the dialog it holds.
	ReadPane(ctx context.Context, id string) (Pane, error)
	// Answer picks the option answer names in the dialog a session is
	// holding, or types answer when it names none. It fails with
	// ErrNoDialog when the pane holds no dialog, and ErrDialogRefused when
	// the dialog is guarded or no keystroke can answer it.
	Answer(ctx context.Context, id, answer string) (Answered, error)
	// Messages is what a session has been sent, newest first, as filter
	// keeps it. A message dropped before it was delivered, or replaced by a
	// later one on its subject, is left out; delivered messages are kept
	// only for the board's retention window.
	Messages(ctx context.Context, id string, filter MessageFilter) ([]QueuedMessage, error)
}

// Pane is one ReadPane of a session.
type Pane struct {
	// Session is the row, with Status read off the pane when it is live.
	Session SessionInfo
	// Text is the visible pane without terminal escapes, or, when Live is
	// false, the screen the session was last captured on.
	Text string
	Live bool
	// Dialog is the dialog the live pane is holding, guarded ones
	// included, or nil.
	Dialog *Dialog
}

// Answered is what one Answer did.
type Answered struct {
	// Question is the dialog as it was put, before the answer.
	Question string
	Answer   string
	// Selected is the option the answer picked, or "" when it was typed.
	Selected string
	// Standing is how many questions of the same dialog are still
	// unanswered; answering one of several moves to the next.
	Standing int
}

var (
	// ErrNoDialog is a pane holding no dialog: somebody answered it first,
	// or the session is at its own input line and takes words instead.
	ErrNoDialog = dialog.ErrNoDialog
	// ErrDialogRefused is a dialog no answer from the board may be keyed
	// into. Dialog.Refusal says which kind and why.
	ErrDialogRefused = dialog.ErrNotKeyAnswerable
)
