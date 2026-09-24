package extensionhost

import (
	"context"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/store"
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

func (b *Board) Messages(ctx context.Context, id string, filter extension.MessageFilter) ([]extension.QueuedMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	from := filter.From
	switch from {
	case extension.SenderOperator:
		from = store.HumanSenderID
	case extension.SenderRelayed:
		from = store.RelayedHumanSenderID
	}
	got, err := b.cmds.BoardMessages(id, store.InboxFilter{SenderID: from, Pending: filter.Pending, Limit: filter.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]extension.QueuedMessage, 0, len(got))
	for _, msg := range got {
		sender := msg.SenderID
		switch sender {
		case store.HumanSenderID, "":
			sender = extension.SenderOperator
		case store.RelayedHumanSenderID:
			sender = extension.SenderRelayed
		}
		out = append(out, extension.QueuedMessage{
			ID:          msg.ID,
			From:        sender,
			Subject:     msg.Subject,
			Text:        msg.Body,
			QueuedAt:    msg.SentAt,
			DeliveredAt: msg.DeliveredAt,
		})
	}
	return out, nil
}
