package extensionhost

import (
	"context"
	"fmt"
	"regexp"

	"github.com/usestring/gate-inbox/extension"
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

var rolePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// LaunchFor starts a session for the extension with id: its role is
// recorded under that id, so no extension can wear another's.
func (b *Board) LaunchFor(ctx context.Context, id string, req extension.LaunchRequest) (extension.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return extension.SessionInfo{}, err
	}
	role := ""
	if req.Role != "" {
		if !rolePattern.MatchString(req.Role) {
			return extension.SessionInfo{}, fmt.Errorf("role %q must be lower case, start with a letter, and hold only letters, digits, '-' and '_'", req.Role)
		}
		role = id + "/" + req.Role
	}
	created, err := b.cmds.BoardLaunch(sessioncmd.BoardLaunchOptions{
		Tool:      req.Tool,
		Name:      req.Name,
		Prompt:    req.Prompt,
		Directory: req.Directory,
		Model:     req.Model,
		ParentID:  req.ParentID,
		Group:     req.Group,
		Role:      role,
		Args:      req.Args,
	})
	if err != nil {
		return extension.SessionInfo{}, err
	}
	return info(created), nil
}

// SendFor queues a message from the extension with id: it is queued under
// that extension's sender, so no extension can speak as another.
func (b *Board) SendFor(ctx context.Context, id, target string, msg extension.Message) (extension.Sent, error) {
	if err := ctx.Err(); err != nil {
		return extension.Sent{}, err
	}
	sent, err := b.cmds.BoardSend(id, target, msg.Text, msg.Subject, msg.Interrupt)
	if err != nil {
		return extension.Sent{}, err
	}
	return extension.Sent{
		MessageID:     sent.MessageID,
		QueuePosition: sent.QueuePosition,
		Held:          sent.Held,
		Superseded:    sent.Superseded,
	}, nil
}

// Kill ends an agent session's pane on the board's behalf.
func (b *Board) Kill(ctx context.Context, id string) (extension.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return extension.SessionInfo{}, err
	}
	killed, err := b.cmds.BoardKill(id)
	if err != nil {
		return extension.SessionInfo{}, err
	}
	return info(killed), nil
}

// ArchiveFor archives a helper the extension with id launched; a session
// of anyone else's is refused, so no extension can file another's away.
func (b *Board) ArchiveFor(ctx context.Context, id, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := b.cmds.BoardArchive(id, sessionID)
	return err
}
