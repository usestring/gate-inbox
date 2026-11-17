package extension

import "time"

// ViewSource is implemented by the BoardHost a running board lends: it
// reports where on the board the operator is looking, and how they move
// about it. It is a separate interface so that a BoardHost written before it
// still is one; an extension asks for it with a type assertion, and a host
// that does not implement it reports nothing.
type ViewSource interface {
	// OnView calls fn with every ViewEvent until unsubscribe is called.
	// Delivery is Subscribe's: in order, on the subscription's own
	// goroutine, never on the board's update loop.
	OnView(fn func(ViewEvent)) (unsubscribe func())
}

// ViewEvent is the operator moving about the board.
type ViewEvent struct {
	Kind ViewKind
	// Visible is whether the board's pane is the one the operator's
	// terminal is showing, as the board reads it from tmux or from the
	// terminal's focus reports.
	Visible bool
	// Place is where on the board the operator is.
	Place ViewPlace
	// SessionID is the session focused or attached, or on ViewCursor the
	// one the cursor landed on. It is empty in the list, and on a group
	// row.
	SessionID string
	// Rows is how many rows a ViewCursor event moved the cursor, negative
	// for up. It is zero on every other kind.
	Rows int
	// Triage is whether the board is in triage, listing only the sessions
	// waiting on the operator, longest-waiting first.
	Triage bool
	// Filtered is whether a search is narrowing the list, while it is typed
	// or after it is kept.
	Filtered bool
	// Action is what a ViewAction ran: a key map action, by the name an
	// operator's key file binds it under, such as "archive" or "search", or
	// ActionSnippet. It is empty on every other kind.
	Action string
	// Snippet is the menu key of the snippet an ActionSnippet sent, such as
	// "y". It is the key, never the snippet's text or label.
	Snippet string
	// Via is how a ViewAction was asked for.
	Via ViewVia
	At  time.Time
}

// ViewKind says what a ViewEvent reports.
type ViewKind string

const (
	// ViewChanged is Visible, Place, SessionID, Triage or Filtered having
	// changed. The first event a subscriber sees after the board starts is
	// one, so it knows where the operator is before anything moves.
	ViewChanged ViewKind = "changed"
	// ViewCursor is the operator moving the list's cursor with a key.
	// Moves the board makes itself, such as landing on a session it just
	// created, are not reported.
	ViewCursor ViewKind = "cursor"
	// ViewAction is the operator running a board action: a key bound to
	// one, a snippet, or a pick from the quick actions palette. It is
	// reported when the key is pressed, whether or not the action could run
	// on the row it was pressed on. The keys that move the cursor are
	// reported as ViewCursor instead, and a key that reaches a session or a
	// text field, or is typed into a search, is not reported at all.
	ViewAction ViewKind = "action"
)

// ActionSnippet is the Action of a snippet sent from the board.
const ActionSnippet = "snippet"

// ViewVia is how the operator asked for a ViewAction.
type ViewVia string

const (
	// ViaKey is a key bound to the action, or a snippet's own key.
	ViaKey ViewVia = "key"
	// ViaMenu is a snippet picked in the hotkey menu.
	ViaMenu ViewVia = "menu"
	// ViaPalette is an action picked in the quick actions palette.
	ViaPalette ViewVia = "palette"
)

// ViewPlace is where on the board the operator is.
type ViewPlace string

const (
	// ViewList is the session list, with a session's preview beside it.
	ViewList ViewPlace = "list"
	// ViewFocus is a session focused inside the board, keys going to it.
	ViewFocus ViewPlace = "focus"
	// ViewAttach is a session attached full screen, until it detaches back
	// to the board.
	ViewAttach ViewPlace = "attach"
)
