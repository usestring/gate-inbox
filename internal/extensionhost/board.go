package extensionhost

import (
	"context"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

var _ extension.Board = (*Board)(nil)

// Board acts as the board: no calling session, the operator's reach over
// reads and answers.
type Board struct {
	configDir string
	cmds      *sessioncmd.Sessions
}

// NewBoard is a Board over configDir, running commands through cmds.
func NewBoard(configDir string, cmds *sessioncmd.Sessions) *Board {
	return &Board{configDir: configDir, cmds: cmds}
}

func (b *Board) ConfigDir() string { return b.configDir }

func (b *Board) Get(ctx context.Context, id string) (extension.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return extension.SessionInfo{}, err
	}
	got, err := b.cmds.BoardGet(id)
	if err != nil {
		return extension.SessionInfo{}, err
	}
	return info(got), nil
}

func (b *Board) List(ctx context.Context, filter extension.SessionFilter) (extension.SessionList, error) {
	if err := ctx.Err(); err != nil {
		return extension.SessionList{}, err
	}
	list, err := b.cmds.BoardList(listOptions(filter))
	if err != nil {
		return extension.SessionList{}, err
	}
	return sessionList(list), nil
}

func (b *Board) ReadPane(ctx context.Context, id string) (extension.Pane, error) {
	if err := ctx.Err(); err != nil {
		return extension.Pane{}, err
	}
	read, err := b.cmds.BoardRead(id)
	if err != nil {
		return extension.Pane{}, err
	}
	pane := extension.Pane{Session: info(read.Session), Text: read.Text, Live: read.Live}
	// Read again through the public parser rather than converted field by
	// field, so the two can never disagree about the same screen.
	if read.HasDialog {
		if held, ok := extension.InspectDialog(read.Text); ok {
			pane.Dialog = &held
		}
	}
	return pane, nil
}

func (b *Board) Answer(ctx context.Context, id, answer string) (extension.Answered, error) {
	if err := ctx.Err(); err != nil {
		return extension.Answered{}, err
	}
	got, err := b.cmds.BoardAnswer(id, answer)
	if err != nil {
		return extension.Answered{}, err
	}
	return extension.Answered{
		Question: got.Question,
		Answer:   got.Answer,
		Selected: got.Selected,
		Standing: got.Standing,
	}, nil
}

func (b *Board) Transcript(ctx context.Context, id string) (extension.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return extension.Transcript{}, err
	}
	return transcriptOf(b.cmds.BoardTranscript(id))
}

func (b *Board) Handover(ctx context.Context, id string, opts extension.HandoverOptions) (extension.Handover, error) {
	if err := ctx.Err(); err != nil {
		return extension.Handover{}, err
	}
	return handoverOf(b.cmds.BoardHandover(id, handoverOptions(opts)))
}

func transcriptOf(got migrate.Transcript, err error) (extension.Transcript, error) {
	if err != nil {
		return extension.Transcript{}, err
	}
	return extension.Transcript{Kind: got.Kind, Path: got.Path, Command: got.Command}, nil
}

func handoverOptions(opts extension.HandoverOptions) sessioncmd.BoardHandoverOptions {
	return sessioncmd.BoardHandoverOptions{Until: opts.Until, Dest: opts.Dest}
}

func handoverOf(got sessioncmd.HandoverCopy, err error) (extension.Handover, error) {
	if err != nil {
		return extension.Handover{}, err
	}
	return extension.Handover{Path: got.Path, Note: got.Note, Cut: got.Cut, Filtered: got.Filtered}, nil
}
