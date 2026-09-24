package app

import (
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/ui"
)

type closedRecorder struct {
	extension.View
	reasons []extension.CloseReason
}

func (c *closedRecorder) Closed(reason extension.CloseReason) { c.reasons = append(c.reasons, reason) }

// The adapter hands every one of the board's reasons to an extension's
// Closer spelled as the extension package spells it, and a view with no
// Closed is simply not told.
func TestTheBoardsCloseReasonsReachAnExtensionAsItsOwn(t *testing.T) {
	pairs := map[ui.CloseReason]extension.CloseReason{
		ui.CloseDismissed: extension.CloseDismissed,
		ui.CloseSubmitted: extension.CloseSubmitted,
		ui.CloseReturned:  extension.CloseReturned,
		ui.CloseHandle:    extension.CloseHandle,
		ui.CloseReplaced:  extension.CloseReplaced,
	}
	for board, want := range pairs {
		view := &closedRecorder{}
		uiView{view}.Closed(board)
		if len(view.reasons) != 1 || view.reasons[0] != want {
			t.Errorf("%s reached the extension as %v, want %s", board, view.reasons, want)
		}
	}
	uiView{struct{ extension.View }{}}.Closed(ui.CloseDismissed)
}
