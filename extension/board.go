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
// acts, through Host; the one exception is BoardHost.Launch, which starts
// a board extension's own helpers.
//
// An answer through Board is held to the dialog's own rules: a guarded
// dialog is refused here as it is to a session.
type Board interface {
	// ConfigDir is the operator's config directory.
	ConfigDir() string
	SessionReader
	// ReadPane is what a session's pane is showing and the dialog it holds.
	ReadPane(ctx context.Context, id string) (Pane, error)
	// Answer picks the option answer names in the dialog a session is
	// holding, or types answer when it names none. It fails with
	// ErrNoDialog when the pane holds no dialog, and ErrDialogRefused when
	// the dialog is guarded or no keystroke can answer it.
	Answer(ctx context.Context, id, answer string) (Answered, error)
	// Transcript is where a session's conversation can be read. It fails
	// for a session whose conversation id has not been captured yet, and
	// for a tool whose conversations the board cannot locate.
	Transcript(ctx context.Context, id string) (Transcript, error)
	// Handover writes a copy of a session's transcript filtered for a
	// replacement agent to read, the copy a migration hands over. It fails
	// for a Transcript with no Path.
	Handover(ctx context.Context, id string, opts HandoverOptions) (Handover, error)
	// Messages is what a session has been sent, newest first, as filter
	// keeps it. A message dropped before it was delivered, or replaced by a
	// later one on its subject, is left out; delivered messages are kept
	// only for the board's retention window.
	Messages(ctx context.Context, id string, filter MessageFilter) ([]QueuedMessage, error)
	// Tools are the CLIs the config declares, sorted by name, read afresh
	// on each call, so a launch can be checked before it is attempted.
	Tools(ctx context.Context) ([]ToolInfo, error)
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
	// AtPrompt is a live session resting at its input line with nothing
	// written there and nobody typing: the state BoardHost.Command needs
	// to type. It is false on a dialog, while the pane is scrolled into
	// its history, for a stored screen, and for a tool whose input line
	// the board cannot find.
	AtPrompt bool
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
