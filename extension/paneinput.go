package extension

import "time"

// PaneInputSource is implemented by the BoardHost a running board lends: it
// reports what the board hands a session's pane as keys and pastes. It is a
// separate interface so that a BoardHost written before it still is one; an
// extension asks for it with a type assertion, and a host that does not
// implement it reports nothing.
type PaneInputSource interface {
	// OnPaneInput calls fn with every PaneInput until unsubscribe is called.
	// Delivery is Subscribe's: in order, on the subscription's own
	// goroutine, never on the board's update loop.
	OnPaneInput(fn func(PaneInput)) (unsubscribe func())
}

// PaneInput is one key or paste the board handed a session's pane, reported
// once it reached the pane: a key typed into a focused pane, a paste into
// one, or a snippet the board typed in. The board keeps none of it after;
// an extension that keeps Text is keeping what the operator typed.
type PaneInput struct {
	SessionID string
	// Text is the characters a key typed, or the text pasted. It is empty
	// for a key that types nothing, which Key names instead.
	Text string
	// Key is a key that types nothing, by its tmux name: "BSpace", "Left",
	// "Enter", "C-w". Alt adds an "M-" prefix, and turns a character into
	// a named key, so alt+b is "M-b". It is empty when Text is set.
	Key string
	// Paste is set when Text arrived as a paste. A snippet the board types
	// into the pane is one, whether or not it is sent with it.
	Paste bool
	// Dialog is whether the pane held a dialog when the input reached it.
	Dialog bool
	// Sends is set on input the board took as sending the pane's line or
	// answering its dialog: Enter, a key that picks a dialog's option, or a
	// snippet sent whole. Once the pane is seen to take it, the board
	// reports it as an OperatorInput too, with the same At.
	Sends bool
	// Ready is when the board was free for this key: done with the key
	// before it in the same pane, having drawn its screen after that key, or
	// that key's own At when no screen was drawn in between. A key that
	// arrives just after Ready was waiting while the board worked. Ready is
	// zero on a paste, and on a key with no key before it in the pane since
	// the last paste or since the operator typed into another pane.
	Ready time.Time
	At    time.Time
}
