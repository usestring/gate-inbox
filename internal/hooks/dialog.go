package hooks

import (
	"os"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/status"
)

// DialogOpen reports whether a session's own hooks say a dialog is on its
// screen. The board sends such a session no keys of its own, whatever its
// pane shows. The pane rules see a dialog only by a legend they know, so one
// drawn with a new legend or scrolled out of view reads to them as a resting
// prompt, and a paste there becomes the dialog's answer.
//
// source is the status_source of the session's tool. transcript returns a
// Claude Code session's transcript path, or "" when none is known. It is
// called only once the status log says a dialog is up.
func (m *Manager) DialogOpen(source, id string, transcript func() string) bool {
	switch source {
	case StatusSourcePi:
		return m.PromptOpen(id)
	case StatusSourceClaude:
		return m.claudeDialogOpen(id, transcript)
	}
	return false
}

// claudeDialogOpen reads a Claude Code dialog off the status log. Two hook
// events log waiting: a question's PreToolUse and a permission or
// elicitation Notification (launchHooks). The answer logs PostToolUse.
//
// Escape closes a dialog and fires no hook, so the log still says waiting
// at a prompt that would take a message. The transcript records the Escape
// as a rejected tool result and an interrupted line, which close the turn.
// So waiting holds while the turn is open, and while the transcript has not
// been written since the log. In that case the dialog belongs to a
// conversation this transcript does not hold: one started by a /clear the
// row did not follow, or a background agent's. Anything unreadable holds,
// because the log alone cannot tell an open dialog from a closed one.
func (m *Manager) claudeDialogOpen(id string, transcript func() string) bool {
	ev, ok := m.ReadEvent(id)
	if !ok || ev.State != status.Waiting || (ev.Name != "PreToolUse" && ev.Name != "Notification") {
		return false
	}
	path := transcript()
	if path == "" || convo.ClaudeTurnOpen(path) {
		return true
	}
	logged, err := os.Stat(m.StatusFile(id))
	if err != nil {
		return true
	}
	written, err := os.Stat(path)
	if err != nil {
		return true
	}
	return !written.ModTime().After(logged.ModTime())
}
