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
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

type MigrateOptions struct {
	// Tool is the agent CLI the conversation moves to.
	Tool string
	// Name names the new session; empty takes "<source name>-<tool>".
	Name string
	// SkipPermissions and ChildrenSkipPermissions replace the source's
	// settings on the moved session; nil keeps them. An agent may turn either
	// off, but on only where it already is or where the caller spawned the
	// source and the operator lets its children skip permissions: the same
	// line create_session draws, so a move is no way around it.
	SkipPermissions         *bool
	ChildrenSkipPermissions *bool
	// accountOverride is the account a switch_account relaunch has already
	// chosen. An ordinary migration has none and takes none from its
	// caller: the build's account chooser picks (see accounts.Route).
	accountOverride *string
}

// Migrate starts the source session's conversation over on another agent
// CLI: a new session on that tool, in the source's group and directory,
// whose first prompt points at the source's transcript and says to read it
// and carry on. The source is left as it is -- still running if it was --
// so the operator decides when it goes. An account switch is the exception:
// the conversation is moving off the old account, so once the new session
// is up it takes over the source's children and their queued reports, and
// the source is ended and archived; a switch that fails first leaves the
// source as it was. An adopted source, a pane the manager did not start,
// and a source migrating itself are left running with their children even
// then, for whoever is at them to end.
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
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return Session{}, err
	}
	source, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	if err := mayRaiseSkipPermissions(caller, source, opts); err != nil {
		return Session{}, err
	}
	// Whether the source is still running goes into the brief and the
	// migration hook, and an adopted source only answers once reached.
	if err := runtime.reach(source); err != nil {
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
	retire := opts.accountOverride != nil && source.TmuxPaneID == "" && source.ID != sessionID
	prompt := migrate.Prompt(migrate.Brief{
		Source:        source,
		SourceTool:    source.Tool,
		Transcript:    transcript,
		SourceRunning: !retire && runtime.driver.Exists(source.ID) && !source.Archived,
		ReadAction:    words.Read,
		SendAction:    words.Send,
		FilterNote:    filterNote,
	})
	// A migration carries the source's model across. Its account is the
	// build's account chooser's, or the CLI's own login without one.
	id := uuid.NewString()[:8]
	// Before an account is chosen, so a refusal leaves nothing behind.
	shape, err := sessionhooks.Shape(migrate.NewSession(id, name, toolName, source, launch.Plan{Model: source.Model}), extension.LaunchMigrate, source.ID)
	if err != nil {
		return Session{}, err
	}
	prompt = shape.Shaped(prompt)
	var account string
	if opts.accountOverride != nil {
		account = *opts.accountOverride
	} else {
		account, err = accounts.Route(tool, accounts.Request{SessionID: id, ToolName: toolName, Reason: extension.LaunchMigrate, From: source.ID})
	}
	if err != nil {
		return Session{}, err
	}
	plan, err := launch.Assemble(toolName, tool, prompt, "", false, source.Model, account)
	if err != nil {
		return Session{}, err
	}
	sess := migrate.NewSession(id, name, toolName, source, plan)
	sess.SkipPermissions, sess.ChildrenSkipPermissions, err = migrate.SkipPermissions(source, toolName, tool, opts.SkipPermissions, opts.ChildrenSkipPermissions)
	if err != nil {
		return Session{}, err
	}
	base, err := launch.WithSkipPermissions(toolName, tool, plan.Command, sess.SkipPermissions)
	if err != nil {
		return Session{}, err
	}
	sessionHooks, err := sessionhooks.Current()
	if err != nil {
		return Session{}, err
	}
	contributed, err := sessionhooks.Env(sessionHooks, sess, extension.LaunchMigrate, source.ID, nil)
	if err != nil {
		return Session{}, err
	}
	command, env, err := launch.EnvironmentWithProfile(hooks.NewManager(s.configDir), toolName, tool, base, id, plan.Model, plan.Account, sess.SkipPermissions, contributed, mcpProfile(sess))
	if err != nil {
		return Session{}, err
	}
	launched := false
	if err := runtime.store.LaunchSessionBeside(sess, source.ID, func() error {
		err := runtime.createPane(sess.ID, sess.Cwd, command, env)
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
	accounts.RecordLaunch(sess.ID, sess.Tool, sess.Account)
	_ = runtime.driver.SetLabel(sess.ID, sessionLabel(sess.Group, sess.Name))
	if err := retireMigrated(s, runtime, source, sess, retire); err != nil {
		return Session{}, err
	}
	return runtime.sessionInfo(sess, true, false), nil
}

// retireMigrated hands the source's children to moved and files the source
// away, then ends its pane. Only once moved's pane is up: a new session that
// died on launch has taken nothing over, and the source stays as it was.
func retireMigrated(s *Sessions, runtime *runtime, source, moved store.Session, retire bool) error {
	if !retire || !runtime.driver.Exists(moved.ID) {
		return nil
	}
	if err := runtime.store.CompleteMigration(source.ID, moved.ID); err != nil {
		return fmt.Errorf("%s started, but %s's children could not be handed to it: %w", moved.Name, source.Name, err)
	}
	if err := s.endSession(runtime, source, store.EndKilled); err != nil {
		return fmt.Errorf("%s took over, but ending %s failed: %w", moved.Name, source.Name, err)
	}
	return nil
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
	stats, filtered, err := filterTranscript(transcript, dst, handover.DefaultOptions())
	if !filtered {
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

// filterTranscript writes the handover filter's copy of transcript to dst.
// filtered is false for a layout the filter cannot read, and then nothing is
// written.
func filterTranscript(transcript migrate.Transcript, dst string, opts handover.Options) (stats handover.Stats, filtered bool, err error) {
	switch transcript.Kind {
	case "claude":
		stats, err = handover.Claude(transcript.Path, dst, opts)
	case "codex":
		stats, err = handover.Codex(transcript.Path, dst, opts)
	default:
		return handover.Stats{}, false, nil
	}
	return stats, true, err
}

// mayRaiseSkipPermissions refuses a move that would turn a session's prompts
// off where the operator has not: an agent may keep or narrow either setting,
// and raise one only on a session it spawned while its own children are
// allowed to skip permissions.
func mayRaiseSkipPermissions(caller, source store.Session, opts MigrateOptions) error {
	allowed := caller.ChildrenSkipPermissions && store.SpawnerOf(source) == caller.ID
	for _, c := range []struct {
		flag    string
		asked   *bool
		current bool
	}{
		{"skip_permissions", opts.SkipPermissions, source.SkipPermissions},
		{"children_skip_permissions", opts.ChildrenSkipPermissions, source.ChildrenSkipPermissions},
	} {
		if c.asked != nil && *c.asked && !c.current && !allowed {
			return fmt.Errorf("%s true is refused: %s does not have it on, and the operator has not allowed "+
				"this session's children to skip permissions; leave it unset or false", c.flag, source.ID)
		}
	}
	return nil
}
