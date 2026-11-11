package dialog

import "strings"

// Pi's dialogs.
//
// Pi has no permission prompt of its own. The board's pi extension draws one
// for every call the board asks about (package pigate), in the shape Claude
// Code draws its own: a rule as its top edge, the title, the call behind a
// gutter, "Do you want to proceed?", numbered choices under the marker, and
// "Enter to confirm · Esc to cancel". So the readers that already know
// Claude's prompt read it -- Inspect as KindApproval, which is guarded, and
// ReadScreen as ScreenPermission -- and the board never answers it on its
// own say.
//
// The pi reader adds one thing: a dialog drawn with the extension's legend is
// a permission prompt whatever its text says, since the extension draws no
// other dialog with it, so a command whose words happen to read like another
// kind of dialog cannot change how it is read.

// piPermissionLegend closes the extension's permission dialog.
const piPermissionLegend = "Enter to confirm · Esc to cancel"

// ReadPiScreen reads the dialog a pi pane is holding.
func ReadPiScreen(pane string) (Screen, bool) {
	screen, ok := ReadScreen(pane)
	if !ok {
		return Screen{}, false
	}
	if strings.TrimSpace(screen.Legend) == piPermissionLegend {
		screen.Kind = ScreenPermission
	}
	return screen, true
}

func init() {
	RegisterTool("pi", ToolReader{Screen: ReadPiScreen})
}
