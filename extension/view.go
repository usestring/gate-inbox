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
	// for up. It is zero on ViewChanged.
	Rows int
	At   time.Time
}

// ViewKind says what a ViewEvent reports.
type ViewKind string

const (
	// ViewChanged is Visible, Place or SessionID having changed. The first
	// event a subscriber sees after the board starts is one, so it knows
	// where the operator is before anything moves.
	ViewChanged ViewKind = "changed"
	// ViewCursor is the operator moving the list's cursor with a key.
	// Moves the board makes itself, such as landing on a session it just
	// created, are not reported.
	ViewCursor ViewKind = "cursor"
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
