package sessioncmd

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// BoardLaunchOptions is a session a board extension launches for itself.
type BoardLaunchOptions struct {
	Tool      string
	Name      string
	Prompt    string
	Directory string
	Model     string
	// ParentID files the session under that one, as a leaf.
	ParentID string
	// Group is where a session with no parent goes.
	Group string
	// Role is recorded as it is given, already qualified by the extension's
	// ID.
	Role string
	// Args ride the command line after the prompt, each quoted as one word.
	Args []string
}

// BoardLaunch starts an agent session on the board's behalf rather than a
// calling session's: the one write the board lends code running beside it,
// so an extension can start the helpers it works through.
//
// It is filed as a leaf under its parent, the way a terminal is, so a
// parent that is itself somebody's child can still have one; nothing is
// ever filed under it in turn. It is not auto-named: a helper's name is its
// launcher's to choose, and a turn spent choosing one is spend on nothing.
func (s *Sessions) BoardLaunch(opts BoardLaunchOptions) (created Session, err error) {
	op := start("sessioncmd.board.launch")
	defer func() {
		op.done(&err,
			tracing.Attr{Key: "tool", Value: opts.Tool},
			tracing.Attr{Key: "created", Value: created.ID})
	}()
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	toolName := strings.TrimSpace(opts.Tool)
	tool, known := runtime.cfg.Tools[toolName]
	if !known {
		return Session{}, fmt.Errorf("tool %q is not configured; configured tools are %s", toolName, strings.Join(agentToolNames(runtime), ", "))
	}
	if tool.Shell {
		return Session{}, fmt.Errorf("tool %q opens a shell, not an agent", toolName)
	}
	var group, dir string
	if parentID := strings.TrimSpace(opts.ParentID); parentID != "" {
		parent, err := runtime.agent(parentID)
		if err != nil {
			return Session{}, err
		}
		group = parent.Group
		dir = parent.Cwd
		if strings.TrimSpace(opts.Directory) != "" {
			if dir, err = resolveTerminalDirectory(opts.Directory); err != nil {
				return Session{}, err
			}
		}
	} else {
		requested := opts.Group
		if group, dir, err = runtime.createTarget(store.Session{}, &requested, opts.Directory); err != nil {
			return Session{}, err
		}
	}
	id := uuid.NewString()[:8]
	name := strings.ReplaceAll(strings.TrimSpace(opts.Name), "/", "-")
	if name == "" {
		name = toolName + "-" + id[:4]
	}
	sess := store.Session{
		ID:         id,
		Name:       name,
		Tool:       toolName,
		Cwd:        dir,
		Group:      group,
		Status:     status.Starting,
		ParentID:   strings.TrimSpace(opts.ParentID),
		Model:      strings.TrimSpace(opts.Model),
		NameSource: store.SourceUser,
		Role:       opts.Role,
	}
	prepared, err := s.prepareBoardLaunch(runtime, sess, tool, opts.Prompt, opts.Args, "")
	if err != nil {
		return Session{}, err
	}
	sess = prepared.sess
	create := runtime.store.LaunchSession
	if sess.ParentID != "" {
		create = runtime.store.LaunchSessionLeaf
	}
	launched := false
	if err := create(sess, func() error {
		err := runtime.driver.Create(sess.ID, sess.Cwd, prepared.command, prepared.env, 0, 0)
		launched = err == nil
		return err
	}); err != nil {
		if launched {
			_ = runtime.driver.Kill(sess.ID)
		}
		return Session{}, err
	}
	accounts.RecordLaunch(runtime.store, sess.ID, sess.Tool, sess.Account)
	sessionhooks.Spawned(prepared.hooks, sess, extension.SpawnByExtension)
	_ = runtime.driver.SetLabel(sess.ID, sessionLabel(sess.Group, sess.Name))
	return runtime.sessionInfo(sess, true, false), nil
}

// boardLaunch is a board extension's session made ready to start: the row
// as it will be filed, and the pane's command and environment.
type boardLaunch struct {
	sess    store.Session
	command string
	env     map[string]string
	hooks   *extension.SessionHooks
}

// prepareBoardLaunch asks the spawn policies about sess, picks its account
// and assembles its pane: the part BoardLaunch and BoardReplace share. from
// is the session a replacement stands in for, and empty for a launch.
func (s *Sessions) prepareBoardLaunch(runtime *runtime, sess store.Session, tool config.Tool, prompt string, args []string, from string) (boardLaunch, error) {
	sessionHooks, err := sessionhooks.CheckSpawn(sess, extension.SpawnByExtension)
	if err != nil {
		return boardLaunch{}, err
	}
	account, err := runtime.accountOr(sess.Account, tool, sess.ID)
	if err != nil {
		return boardLaunch{}, err
	}
	plan, err := launch.Assemble(sess.Tool, tool, strings.TrimSpace(prompt), "", false, sess.Model, account)
	if err != nil {
		return boardLaunch{}, err
	}
	for _, arg := range args {
		plan.Command += " " + tmux.ShellQuote(arg)
	}
	sess.AgentSessionID = plan.AgentSessionID
	sess.PendingInputs = plan.PendingInputs
	sess.LaunchPrompt = plan.LaunchPrompt
	sess.Model = plan.Model
	sess.Account = plan.Account
	reason := extension.LaunchSpawn
	if from != "" {
		reason = extension.LaunchReplace
	}
	contributed, err := sessionhooks.Env(sessionHooks, sess, reason, from)
	if err != nil {
		return boardLaunch{}, err
	}
	command, env, err := launch.Environment(hooks.NewManager(s.configDir), sess.Tool, tool, plan.Command, sess.ID, plan.Model, plan.Account, contributed)
	if err != nil {
		return boardLaunch{}, err
	}
	return boardLaunch{sess: sess, command: command, env: env, hooks: sessionHooks}, nil
}
