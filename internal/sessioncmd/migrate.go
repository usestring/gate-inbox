// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/handover"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/tracing"
)

type MigrateOptions struct {
	// Tool is the agent CLI the conversation moves to.
	Tool string
	// Name names the new session; empty takes "<source name>-<tool>".
	Name string
	// Account is the named subscription the new session runs on; empty
	// keeps the source's, which is how a conversation that has hit one
	// person's limit is moved onto another's window.
	Account string
	// An account switch can explicitly select own login, unlike an ordinary migration's empty default.
	accountOverride *string
}

// Migrate starts the source session's conversation over on another agent
// CLI: a new session on that tool, in the source's group and directory,
// whose first prompt points at the source's transcript and says to read it
// and carry on. The source is left as it is -- still running if it was --
// so the operator decides when it goes.
func (s *Sessions) Migrate(sessionID, targetID string, opts MigrateOptions) (moved Session, err error) {
	// A migration reads the source CLI's whole transcript off disk, rewrites
	// it for the destination, and launches that CLI on it, which makes this
	// the longest thing an agent can ask for and wait on.
	op := start("sessioncmd.migrate", sessionAttr(targetID))
	defer func() {
		op.done(&err,
			tracing.Attr{Key: "tool", Value: opts.Tool},
			tracing.Attr{Key: "created", Value: moved.ID})
	}()
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Session{}, err
	}
	source, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	toolName := strings.TrimSpace(opts.Tool)
	if toolName == "" {
		return Session{}, fmt.Errorf("tool is empty; name the agent CLI to move %s to, one of %s", source.ID, strings.Join(agentToolNames(runtime), ", "))
	}
	tool, known := runtime.cfg.Tools[toolName]
	if !known {
		return Session{}, fmt.Errorf("tool %q is not configured; configured tools are %s", toolName, strings.Join(agentToolNames(runtime), ", "))
	}
	if tool.Shell {
		return Session{}, fmt.Errorf("tool %q opens a shell, not an agent; a conversation cannot move there", toolName)
	}
	sourceTool, known := runtime.cfg.Tools[source.Tool]
	if !known {
		return Session{}, fmt.Errorf("tool %s is no longer configured", source.Tool)
	}
	if _, err := resolveTerminalDirectory(source.Cwd); err != nil {
		return Session{}, fmt.Errorf("working directory no longer exists: %s", source.Cwd)
	}
	transcript, err := migrate.Locate(s.roots, source.Tool, sourceTool, source)
	if err != nil {
		return Session{}, err
	}
	transcript, filterNote, err := s.filterForHandover(transcript)
	if err != nil {
		return Session{}, err
	}
	name := strings.ReplaceAll(strings.TrimSpace(opts.Name), "/", "-")
	if name == "" {
		name = source.Name + "-" + toolName
	}
	words := VocabularyFor(toolName, tool)
	prompt := migrate.Prompt(migrate.Brief{
		Source:        source,
		SourceTool:    source.Tool,
		Transcript:    transcript,
		SourceRunning: runtime.driver.Exists(source.ID) && !source.Archived,
		ReadAction:    words.Read,
		SendAction:    words.Send,
		FilterNote:    filterNote,
	})
	// A migration carries the source's model and account across when the
	// destination can take them, and refuses rather than quietly dropping
	// either when it cannot.
	// The named account, else the source's, else the board's default -- on
	// a destination that can take one; a move to a CLI that cannot runs on
	// that CLI's own login rather than refusing.
	named := opts.Account
	if opts.Account == "" && source.Account != "" && tool.AccountEnv != "" {
		named = source.Account
	}
	id := uuid.NewString()[:8]
	// Before an account is borrowed, so a refusal leaves nothing behind.
	shape, err := sessionhooks.Shape(migrate.NewSession(id, name, toolName, source, launch.Plan{Model: source.Model}), extension.LaunchMigrate, source.ID)
	if err != nil {
		return Session{}, err
	}
	prompt = shape.Prefixed(prompt)
	var account string
	if opts.accountOverride != nil {
		account = *opts.accountOverride
		err = accounts.CarryBorrower(runtime.store, source.ID, id)
	} else {
		account, err = runtime.accountOr(named, tool, id)
	}
	if err != nil {
		return Session{}, err
	}
	plan, err := launch.Assemble(toolName, tool, prompt, "", false, source.Model, account)
	if err != nil {
		return Session{}, err
	}
	sess := migrate.NewSession(id, name, toolName, source, plan)
	sessionHooks, err := sessionhooks.Current()
	if err != nil {
		return Session{}, err
	}
	contributed, err := sessionhooks.Env(sessionHooks, sess, extension.LaunchMigrate, source.ID)
	if err != nil {
		return Session{}, err
	}
	command, env, err := launch.Environment(hooks.NewManager(s.configDir), toolName, tool, plan.Command, id, plan.Model, plan.Account, contributed)
	if err != nil {
		return Session{}, err
	}
	launched := false
	if err := runtime.store.LaunchSessionBeside(sess, source.ID, func() error {
		err := runtime.driver.Create(sess.ID, sess.Cwd, command, env, 0, 0)
		launched = err == nil
		return err
	}); err != nil {
		if launched {
			_ = runtime.driver.Kill(sess.ID)
		}
		return Session{}, err
	}
	// An extension that keeps state for the source has to carry it across
	// before the migration counts as done; one that cannot undoes it, so
	// the operator is never left with a new session its extensions do not
	// know about.
	if err := sessionhooks.Migrated(sessionHooks, source, sess, runtime.driver.Exists(source.ID)); err != nil {
		_ = runtime.driver.Kill(sess.ID)
		_ = runtime.store.Delete(sess.ID)
		return Session{}, err
	}
	accounts.RecordLaunch(runtime.store, sess.ID, sess.Tool, sess.Account)
	_ = runtime.driver.SetLabel(sess.ID, sessionLabel(sess.Group, sess.Name))
	return runtime.sessionInfo(sess, true, false), nil
}

// filterForHandover runs the handover filter over a located transcript,
// writing the filtered copy the migration hands over.
//
// It runs on every format the manager can parse, and it is fail-closed on
// purpose: the one thing this filter exists to prevent is a replacement
// reading back a stuck agent's transcript whole, so an error is a reason to
// refuse the migration, not a reason to hand over the raw file. Formats the
// manager cannot filter (opencode's export) are handed over as they
// are, as before.
func (s *Sessions) filterForHandover(transcript migrate.Transcript) (migrate.Transcript, string, error) {
	if transcript.Kind == "" || transcript.Path == "" {
		return transcript, "", nil
	}
	dst := transcript.Path + ".handover.jsonl"
	var stats handover.Stats
	var err error
	switch transcript.Kind {
	case "claude":
		stats, err = handover.Claude(transcript.Path, dst, handover.DefaultOptions())
	case "codex":
		stats, err = handover.Codex(transcript.Path, dst, handover.DefaultOptions())
	default:
		return transcript, "", nil
	}
	if err != nil {
		return transcript, "", fmt.Errorf("handover filter failed for %s: %w; refusing the migration rather than handing over the raw transcript", transcript.Path, err)
	}
	if stats.Empty() {
		return transcript, "", nil
	}
	transcript.Path = dst
	return transcript, stats.Note(), nil
}
