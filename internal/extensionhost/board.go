package extensionhost

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"

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
	role, err := qualifiedRole(id, req.Role)
	if err != nil {
		return extension.SessionInfo{}, err
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

// qualifiedRole records role under the extension with id, so no extension
// can wear another's.
func qualifiedRole(id, role string) (string, error) {
	if role == "" {
		return "", nil
	}
	if !rolePattern.MatchString(role) {
		return "", fmt.Errorf("role %q must be lower case, start with a letter, and hold only letters, digits, '-' and '_'", role)
	}
	return id + "/" + role, nil
}

// ReplaceFor starts a session in target's place for the extension with id.
// A role it names is qualified as LaunchFor qualifies one; a session wearing
// another extension's role is that extension's to replace. With hold, the
// returned handle is still to be settled; without, it is already committed.
func (b *Board) ReplaceFor(ctx context.Context, id, target string, req extension.LaunchRequest, hold bool) (extension.SessionInfo, *ReplaceHandle, error) {
	if err := ctx.Err(); err != nil {
		return extension.SessionInfo{}, nil, err
	}
	if req.ParentID != "" || req.Group != "" {
		return extension.SessionInfo{}, nil, errors.New("a replacement takes the old session's place; leave ParentID and Group empty, or Launch a new session instead")
	}
	role, err := qualifiedRole(id, req.Role)
	if err != nil {
		return extension.SessionInfo{}, nil, err
	}
	created, err := b.cmds.BoardReplace(target, id+"/", sessioncmd.BoardLaunchOptions{
		Tool:      req.Tool,
		Name:      req.Name,
		Prompt:    req.Prompt,
		Directory: req.Directory,
		Model:     req.Model,
		Role:      role,
		Args:      req.Args,
	}, hold)
	if err != nil {
		return extension.SessionInfo{}, nil, err
	}
	handle := &ReplaceHandle{cmds: b.cmds, target: target, fresh: created.ID}
	if !hold {
		handle.state = replaceCommitted
	}
	return info(created), handle, nil
}

type replaceState int

const (
	replaceHeld replaceState = iota
	replaceCommitted
	replaceAborted
)

// ReplaceHandle is the board's extension.ReplaceHandle: one replacement,
// held until Commit or Abort settles it.
type ReplaceHandle struct {
	cmds          *sessioncmd.Sessions
	target, fresh string

	mu    sync.Mutex
	state replaceState
}

func (h *ReplaceHandle) Commit(ctx context.Context) error {
	return h.settle(ctx, replaceCommitted, func() error {
		_, err := h.cmds.BoardCommitReplace(h.target, h.fresh)
		return err
	})
}

func (h *ReplaceHandle) Abort(ctx context.Context) error {
	return h.settle(ctx, replaceAborted, func() error {
		return h.cmds.BoardAbortReplace(h.target, h.fresh)
	})
}

// Settled reports whether the handle has been committed or aborted.
func (h *ReplaceHandle) Settled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state != replaceHeld
}

// settle runs act once, under the lock, so a Commit and an Abort racing
// each other settle the handle one way only.
func (h *ReplaceHandle) settle(ctx context.Context, to replaceState, act func() error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch h.state {
	case to:
		return nil
	case replaceHeld:
	default:
		return fmt.Errorf("session %s in %s's place: %w", h.fresh, h.target, extension.ErrReplaceSettled)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := act(); err != nil {
		return err
	}
	h.state = to
	return nil
}
