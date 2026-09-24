package app

import (
	"context"
	"sync"

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

// spawnReader is what a spawn policy reads the board through, in whichever
// process is launching. The board is found at the first read rather than at
// startup, so a command that never spawns never looks for it.
type spawnReader struct {
	once  sync.Once
	board extension.Board
	err   error
}

func (r *spawnReader) open() (extension.Board, error) {
	r.once.Do(func() { r.board, r.err = NewBoard() })
	return r.board, r.err
}

func (r *spawnReader) Get(ctx context.Context, id string) (extension.SessionInfo, error) {
	board, err := r.open()
	if err != nil {
		return extension.SessionInfo{}, err
	}
	return board.Get(ctx, id)
}

func (r *spawnReader) List(ctx context.Context, filter extension.SessionFilter) (extension.SessionList, error) {
	board, err := r.open()
	if err != nil {
		return extension.SessionList{}, err
	}
	return board.List(ctx, filter)
}
