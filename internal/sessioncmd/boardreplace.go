package sessioncmd

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// BoardReplace starts a fresh agent session in targetID's place on a board
// extension's behalf, and ends targetID: a restart that drops the
// conversation and keeps the seat. The new row takes the old one's parent,
// group, spawner and place in the list, and its tool, directory, model,
// account, name and role wherever opts leaves them empty. The messages
// still queued for the old one and its file reservations move across, and
// the old row is left dead with its last screen, as BoardKill leaves it.
//
// The swap is one store transaction with the pane swap inside it, so a poll
// pass reads either the old row alone or the new one beside a dead old one,
// and a failure anywhere leaves the old session running as it was.
//
// rolePrefix is the replacing extension's own role prefix: a session
// wearing some other extension's role is refused.
func (s *Sessions) BoardReplace(targetID, rolePrefix string, opts BoardLaunchOptions) (created Session, err error) {
	op := start("sessioncmd.board.replace", sessionAttr(targetID))
	defer func() {
		op.done(&err, tracing.Attr{Key: "created", Value: created.ID})
	}()
	if opts.ParentID != "" || opts.Group != "" {
		return Session{}, fmt.Errorf("a replacement takes session %s's place; it cannot be filed elsewhere", targetID)
	}
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	old, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	if old.Archived {
		return Session{}, fmt.Errorf("session %s is archived; unarchive it before replacing it", old.ID)
	}
	if old.Role != "" && !strings.HasPrefix(old.Role, rolePrefix) {
		return Session{}, fmt.Errorf("session %s plays %s, another extension's role; only that extension may replace it", old.ID, old.Role)
	}
	toolName := strings.TrimSpace(opts.Tool)
	if toolName == "" {
		toolName = old.Tool
	}
	tool, known := runtime.cfg.Tools[toolName]
	if !known {
		return Session{}, fmt.Errorf("tool %q is not configured; configured tools are %s", toolName, strings.Join(agentToolNames(runtime), ", "))
	}
	if tool.Shell {
		return Session{}, fmt.Errorf("tool %q opens a shell, not an agent", toolName)
	}
	dir := old.Cwd
	if strings.TrimSpace(opts.Directory) != "" {
		dir = opts.Directory
	}
	if dir, err = resolveTerminalDirectory(dir); err != nil {
		return Session{}, err
	}
	sess := store.Session{
		ID:         uuid.NewString()[:8],
		Name:       strings.ReplaceAll(strings.TrimSpace(opts.Name), "/", "-"),
		Tool:       toolName,
		Cwd:        dir,
		Status:     status.Starting,
		SpawnedBy:  store.SpawnerOf(old),
		Model:      strings.TrimSpace(opts.Model),
		NameSource: store.SourceUser,
		Role:       opts.Role,
		// Placement is decided by the store from the old row; these are for
		// the spawn policies, which see the row as it will be filed.
		Group:    old.Group,
		ParentID: old.ParentID,
	}
	if sess.Name == "" {
		sess.Name = old.Name
	}
	if sess.Role == "" {
		sess.Role = old.Role
	}
	// A model or account names something only the old session's tool knows.
	if toolName == old.Tool {
		if sess.Model == "" {
			sess.Model = old.Model
		}
		sess.Account = old.Account
	}
	prepared, err := s.prepareBoardLaunch(runtime, sess, tool, opts.Prompt, opts.Args, old.ID)
	if err != nil {
		return Session{}, err
	}
	sess = prepared.sess
	oldRunning := runtime.driver.Exists(old.ID)
	snapshot := ""
	if oldRunning {
		snapshot, _ = runtime.driver.CapturePane(old.ID)
	}
	launched := false
	moved, err := runtime.store.ReplaceSession(sess, old.ID, snapshot, func() error {
		if err := runtime.driver.Create(sess.ID, sess.Cwd, prepared.command, prepared.env, 0, 0); err != nil {
			return err
		}
		launched = true
		if oldRunning {
			return runtime.driver.Kill(old.ID)
		}
		return nil
	})
	if err != nil {
		if launched {
			_ = runtime.driver.Kill(sess.ID)
		}
		return Session{}, err
	}
	// The old agent died without its session-end hook, so its status file
	// would otherwise decide what a revive of it reads as.
	if err := hooks.NewManager(s.configDir).Remove(old.ID); err != nil {
		logging.Warn("could not clear a replaced session's status file", "session", old.ID, logging.Err(err))
	}
	accounts.RecordLaunch(runtime.store, sess.ID, sess.Tool, sess.Account)
	sessionhooks.Spawned(prepared.hooks, sess, extension.SpawnByExtension)
	logging.Info("session replaced by an extension",
		"from", old.ID, "to", sess.ID, "role", sess.Role,
		"forwarded", moved.Forwarded, "reservations", moved.Reservations)
	sess.Group, sess.ParentID = old.Group, old.ParentID
	_ = runtime.driver.SetLabel(sess.ID, sessionLabel(sess.Group, sess.Name))
	return runtime.sessionInfo(sess, true, false), nil
}
