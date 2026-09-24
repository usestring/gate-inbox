// Package extensionhost is the board's side of extension.Host and
// extension.Board: the public session services, answered by the same
// sessioncmd commands a session's own tools run, and translated into the
// extension package's value types so no internal type crosses the boundary.
package extensionhost

import (
	"context"
	"errors"
	"log/slog"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

var _ extension.Host = (*Host)(nil)

// Host acts as one session, or as an operator's shell, for one extension
// once ForExtension names it.
type Host struct {
	configDir string
	sessions  *sessions
	// owner is the extension the Host is lent to, whose ID a board plan
	// carries; empty until ForExtension.
	owner string
}

// New is a Host acting as sessionID, running commands through cmds. An
// empty sessionID is refused by every service, as a session's own tools
// refuse it.
func New(configDir, sessionID string, cmds *sessioncmd.Sessions) *Host {
	return &Host{configDir: configDir, sessions: &sessions{caller: sessionID, cmds: cmds}}
}

// NewOperator is a Host acting as the operator's shell: no caller, reads
// with the board's reach, every other act refused.
func NewOperator(configDir string, cmds *sessioncmd.Sessions) *Host {
	return &Host{configDir: configDir, sessions: &sessions{operator: true, cmds: cmds}}
}

func (h *Host) ConfigDir() string                  { return h.configDir }
func (h *Host) Caller() string                     { return h.sessions.caller }
func (h *Host) Sessions() extension.SessionService { return h.sessions }

// ForExtension is h lent to the extension with id.
func (h *Host) ForExtension(id string) extension.Host {
	lent := *h
	lent.owner = id
	return &lent
}

func (h *Host) PlanReplace(ctx context.Context, id string, req extension.LaunchRequest) (extension.LaunchPlan, error) {
	if h.owner == "" {
		return extension.LaunchPlan{}, errors.New("this Host is lent to no extension, so there is no role to plan a replacement as")
	}
	return NewBoard(h.configDir, h.sessions.cmds).PlanReplaceFor(ctx, h.owner, id, req)
}

// Logger is the process log unscoped: one Host is shared by every
// extension of a session, and the registry tags each one's lines with its
// id.
func (h *Host) Logger() *slog.Logger { return logging.Slog() }

// Tracer is the board's tracing unscoped, which the registry scopes to each
// extension as it does the log.
func (h *Host) Tracer() extension.Tracer { return tracer{} }

type sessions struct {
	caller string
	// operator reads as the board does rather than as caller, which is
	// empty, so the acts that need a caller are refused by sessioncmd.
	operator bool
	cmds     *sessioncmd.Sessions
}

func (s *sessions) Get(ctx context.Context, id string) (extension.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return extension.SessionInfo{}, err
	}
	var got sessioncmd.Session
	var err error
	if s.operator {
		got, err = s.cmds.BoardGet(id)
	} else {
		got, err = s.cmds.Get(s.caller, id)
	}
	if err != nil {
		return extension.SessionInfo{}, err
	}
	return info(got), nil
}

func (s *sessions) List(ctx context.Context, filter extension.SessionFilter) (extension.SessionList, error) {
	if err := ctx.Err(); err != nil {
		return extension.SessionList{}, err
	}
	var list sessioncmd.SessionList
	var err error
	if s.operator {
		list, err = s.cmds.BoardList(listOptions(filter))
	} else {
		list, err = s.cmds.List(s.caller, listOptions(filter))
	}
	if err != nil {
		return extension.SessionList{}, err
	}
	return sessionList(list), nil
}

func listOptions(filter extension.SessionFilter) sessioncmd.ListOptions {
	return sessioncmd.ListOptions{
		Parent:           filter.ParentID,
		Status:           filter.Status,
		IncludeArchived:  filter.IncludeArchived,
		IncludeTerminals: filter.IncludeTerminals,
		Limit:            filter.Limit,
		After:            filter.After,
	}
}

func sessionList(list sessioncmd.SessionList) extension.SessionList {
	out := extension.SessionList{
		Sessions:  make([]extension.SessionInfo, 0, len(list.Sessions)),
		Matched:   list.Matched,
		Truncated: list.Truncated,
		Cursor:    list.Cursor,
	}
	for _, sess := range list.Sessions {
		out.Sessions = append(out.Sessions, info(sess))
	}
	return out
}

func (s *sessions) Spawn(ctx context.Context, req extension.SpawnRequest) (extension.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return extension.SessionInfo{}, err
	}
	opts := sessioncmd.CreateSessionOptions{
		Tool:      req.Tool,
		Name:      req.Name,
		Directory: req.Directory,
		Prompt:    req.Prompt,
		Model:     req.Model,
	}
	if req.Sibling {
		nest := false
		opts.Nest = &nest
		if req.Group != "" {
			opts.Group = &req.Group
		}
	}
	created, err := s.cmds.Create(s.caller, opts)
	if err != nil {
		return extension.SessionInfo{}, err
	}
	return info(created), nil
}

func (s *sessions) Send(ctx context.Context, id, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.cmds.Send(s.caller, id, text, "", false)
	return err
}

func (s *sessions) Kill(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.cmds.KillOrEndTerminal(s.caller, id, extension.KillByExtension)
	return err
}

func (s *sessions) Archive(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.cmds.Archive(s.caller, id, true)
	return err
}

func (s *sessions) Transcript(ctx context.Context, id string) (extension.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return extension.Transcript{}, err
	}
	return transcriptOf(s.cmds.Transcript(s.caller, id))
}

func (s *sessions) Handover(ctx context.Context, id string, opts extension.HandoverOptions) (extension.Handover, error) {
	if err := ctx.Err(); err != nil {
		return extension.Handover{}, err
	}
	return handoverOf(s.cmds.Handover(s.caller, id, handoverOptions(opts)))
}

func (s *sessions) Read(ctx context.Context, id, since string) (extension.Screen, error) {
	if err := ctx.Err(); err != nil {
		return extension.Screen{}, err
	}
	screen, err := s.cmds.Read(s.caller, id, since)
	if err != nil {
		return extension.Screen{}, err
	}
	session := info(screen.Session)
	// The digest reads the pane in hand; the row is only as fresh as the
	// board's last poll.
	if screen.Digest.Status != "" {
		session.Status = screen.Digest.Status
	}
	return extension.Screen{
		Session:  session,
		Output:   screen.Output,
		Mode:     screen.Mode,
		Cursor:   screen.Cursor,
		Question: screen.Digest.Question,
		Result:   screen.Digest.Result,
	}, nil
}

func info(sess sessioncmd.Session) extension.SessionInfo {
	return extension.SessionInfo{
		ID:        sess.ID,
		Name:      sess.Name,
		Tool:      sess.Tool,
		Model:     sess.Model,
		Group:     sess.Group,
		Directory: sess.Directory,
		Status:    sess.Status,
		Running:   sess.Running,
		Archived:  sess.Archived,
		ParentID:  sess.ParentID,
		SpawnedBy: sess.SpawnedBy,
		Role:      sess.Role,

		AgentSessionID: sess.AgentSessionID,
		Terminal:       sess.Terminal,
		CreatedAt:      sess.CreatedAt,
		ArchivedAt:     sess.ArchivedAt,
		ReplacedBy:     sess.ReplacedBy,
	}
}
