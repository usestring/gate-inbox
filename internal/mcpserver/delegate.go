package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/managerbuild"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// Running a spawn on today's code from a server that is weeks old.
//
// An MCP server is started once by its CLI and lives as long as the
// conversation -- weeks -- while the board is rebuilt and restarted under it
// many times. staleNotice tells the agent about that, which is all a session
// can do about a tool whose ARGUMENTS have moved on. But create_session is
// the one tool where the cost is not the arguments: a server predating the
// nesting path files every spawn with no parent, so the fan-out lands flat,
// the parent is never told when a child stops or finishes, and nothing
// recorded anywhere says which session asked for which child. That is
// unrecoverable after the fact -- there is no creator column and no log line
// to reconstruct it from -- so it has to not happen.
//
// It does not have to happen, because the current code is on disk. The board
// installs itself at launch.Executable() on every start, precisely so a
// weeks-old session can reach current code, and that copy has a CLI over the
// same engine this handler calls. So a stale server runs the spawn there
// instead of in its own process, and the row is filed by today's rules.
//
// revive_session and migrate_session go the same way, for the hook settings
// they launch the pane on rather than the row they file: see reviveSession.
//
// Only when stale, and only for the tools that launch a pane. An in-process
// call is a function call and a delegated one is a process, so paying that on
// every launch from every server to fix the ones that are behind would be the
// wrong trade; and a tool whose staleness costs only a missing argument is
// already served by the notice.

// delegatedLaunchTimeout bounds the child process. A launch opens a pane,
// which takes real time -- but a delegate that never returns must fail the
// call rather than hang the conversation.
const delegatedLaunchTimeout = 3 * time.Minute

// createSession runs a spawn, through the installed manager when this server
// is too old to file the row correctly itself and in process otherwise.
// inProcess is the handler's own path, passed in so the decision is the only
// thing this function owns.
func createSession(configDir, callerID string, opts sessioncmd.CreateSessionOptions,
	inProcess func(string, sessioncmd.CreateSessionOptions) (sessioncmd.Session, error)) (sessioncmd.Session, error) {
	return delegateWhenStale(configDir, callerID, spawnArgs(opts), opts.Tool, func() (sessioncmd.Session, error) {
		return inProcess(callerID, opts)
	})
}

// reviveSession and migrateSession launch a pane the way a spawn does, so a
// stale server running them in process launches it on its own build's hook
// settings. A build from before the parent channel writes settings with no
// prompt-submit hook, and the session it brings back then gets no note on any
// sealed message: a revived child reads every message from its parent as
// unverified. The installed manager writes today's settings.
func reviveSession(configDir, callerID, targetID string,
	inProcess func(string, string) (sessioncmd.Session, error)) (sessioncmd.Session, error) {
	return delegateWhenStale(configDir, callerID, []string{"revive", "--json", "--", targetID}, "",
		func() (sessioncmd.Session, error) { return inProcess(callerID, targetID) })
}

func migrateSession(configDir, callerID, targetID string, opts sessioncmd.MigrateOptions,
	inProcess func(string, string, sessioncmd.MigrateOptions) (sessioncmd.Session, error)) (sessioncmd.Session, error) {
	return delegateWhenStale(configDir, callerID, migrateArgs(targetID, opts), opts.Tool,
		func() (sessioncmd.Session, error) { return inProcess(callerID, targetID, opts) })
}

// delegateWhenStale runs args on the installed manager when this server is
// stale, and inProcess otherwise or when the delegate cannot run.
func delegateWhenStale(configDir, callerID string, args []string, tool string,
	inProcess func() (sessioncmd.Session, error)) (sessioncmd.Session, error) {
	if _, stale := managerbuild.StaleSince(configDir, time.Now()); !stale {
		return inProcess()
	}
	// Installed, not Executable: Executable falls back to this process's own
	// path, and delegating to the stale binary that is asking is the one
	// answer that cannot help.
	installed := launch.Installed()
	if installed == "" {
		return inProcess()
	}
	result, err := delegate(installed, callerID, args, tool)
	if err != nil {
		// The delegate is a repair, not a gate: a launch that cannot be run
		// through it still has to happen, on whatever this build can give it.
		logging.Info("launch not delegated to the installed manager; running it here instead",
			"caller", callerID, "command", args[0], "manager", installed, logging.Err(err))
		return inProcess()
	}
	return result, nil
}

// delegate runs args on the installed manager as this session, and reads the
// row it reports back out of the JSON.
func delegate(manager, callerID string, args []string, tool string) (result sessioncmd.Session, err error) {
	// A launch that runs as a second process, on top of the call it is
	// delegating, and the agent waits for both. Worth its own span because it
	// is invisible from everywhere else: the row it files names the installed
	// manager as nothing at all, and the only sign this path ran is that the
	// call took twice as long.
	if tracing.Enabled() {
		started := time.Now()
		defer func() {
			tracing.Record("mcp.delegated_"+args[0], started, time.Now(), err,
				tracing.Attr{Key: "session", Value: callerID},
				tracing.Attr{Key: "tool", Value: tool},
				tracing.Attr{Key: "created", Value: result.ID})
		}()
	}
	cmd := exec.Command(manager, args...)
	// The CLI takes its caller from the environment, and that caller is what
	// a new row is filed under -- so this is the whole point of the call,
	// not plumbing. Set rather than appended: this process already carries
	// its own value of it, and which of two entries for one name a child
	// reads is not something to leave to the platform.
	cmd.Env = withSessionID(os.Environ(), callerID)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := runBounded(cmd, delegatedLaunchTimeout); err != nil {
		return sessioncmd.Session{}, fmt.Errorf("%w: %s", err, firstLine(stderr.String()))
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return sessioncmd.Session{}, fmt.Errorf("reading the delegated %s's result: %w", args[0], err)
	}
	if result.ID == "" {
		return sessioncmd.Session{}, fmt.Errorf("the delegated %s reported no session", args[0])
	}
	return result, nil
}

// migrateArgs writes a migration's options as the CLI's flags, leaving out
// what was not asked for. The target follows "--", since the CLI reads flags
// wherever they sit and an id is the agent's to type.
func migrateArgs(targetID string, opts sessioncmd.MigrateOptions) []string {
	args := []string{"migrate", "--json"}
	if opts.Tool != "" {
		args = append(args, "--tool", opts.Tool)
	}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	return append(args, "--", targetID)
}

// spawnArgs writes the options as the CLI's flags. A pointer field is only
// passed when it is set: an omitted group inherits the caller's, so spelling
// it out here would turn "not asked for" into an answer.
func spawnArgs(opts sessioncmd.CreateSessionOptions) []string {
	args := []string{"spawn", "--json"}
	// Ordered, not a map: an argument list that reshuffles itself between
	// runs cannot be asserted on, and the one test that matters here is what
	// the delegate is actually asked to do.
	for _, given := range []struct{ flag, value string }{
		{"--name", opts.Name},
		{"--prompt", opts.Prompt},
		{"--tool", opts.Tool},
		{"--model", opts.Model},
		{"--account", opts.Account},
		{"--directory", opts.Directory},
	} {
		if given.value != "" {
			args = append(args, given.flag, given.value)
		}
	}
	if opts.Group != nil {
		args = append(args, "--group", *opts.Group)
	}
	if opts.Nest != nil {
		args = append(args, "--nest="+strconv.FormatBool(*opts.Nest))
	}
	return args
}

// withSessionID replaces any existing caller id in env with this one.
func withSessionID(env []string, callerID string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, hooks.EnvSessionID+"=") {
			out = append(out, entry)
		}
	}
	return append(out, hooks.EnvSessionID+"="+callerID)
}

func runBounded(cmd *exec.Cmd, limit time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		_ = cmd.Process.Kill()
		return fmt.Errorf("the installed manager did not finish within %s", limit)
	}
}

func firstLine(s string) string {
	if i := bytes.IndexByte([]byte(s), '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
