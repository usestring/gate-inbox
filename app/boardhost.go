package app

import (
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/extensionhost"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

// NewBoard is the board of the operator's config directory, as code that
// runs beside it rather than inside one session reads and answers it. It
// holds nothing open: each call opens the config, the store and the tmux
// server it names, and closes them again, so a Board may be kept for the
// life of the process and used from any goroutine.
func NewBoard() (extension.Board, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return extensionhost.NewBoard(dir, sessioncmd.NewSessions(dir, sessioncmd.MCPVocabulary())), nil
}
