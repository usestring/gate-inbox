// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/debugserver"
	"github.com/usestring/gate-inbox/internal/extensionhost"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/managerbuild"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/singleton"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tooldrivers"
	"github.com/usestring/gate-inbox/internal/tracing"
	"github.com/usestring/gate-inbox/internal/ui"
)

// requireTerminal opens the same device Bubble Tea will, so the refusal
// happens here rather than after the incumbent is gone.
func requireTerminal() error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("the board needs a terminal and none is attached (%w); any running board was left alone", err)
	}
	return tty.Close()
}

func runBoard(version string, registry *extension.Registry) error {
	// Checked before anything else because a launch with no terminal fails
	// in Bubble Tea after it has evicted the running board through the
	// singleton lock and rewritten the shared pin journal through the
	// driver's deferred restore -- leaving the operator with no board.
	if err := requireTerminal(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	// A section an extension refuses switches that extension off and
	// nothing else: the board starts, logs it below, and says so in the
	// key map, rather than refusing to start over one optional feature.
	extensionReport := registry.Configure(dir, cfg.Extensions)
	// Refused, unlike an extension's section: a tool block naming an mcp
	// or session_store style this build does not have would launch
	// sessions without the board's tools.
	if err := tooldrivers.CheckTools(cfg.Tools); err != nil {
		return err
	}

	// The log is opened before anything else can fail, because the whole
	// point of it is to have a record of the failures. A log that cannot be
	// opened is not a reason to refuse to start: the manager runs without it.
	logger := openLog(cfg)
	defer logger.Close()
	logging.SetDefault(logger)
	for _, disabled := range extensionReport.Disabled {
		logging.Warn("extension disabled by its config", "extension", disabled.ID, "error", disabled.Err)
	}
	if len(extensionReport.Unknown) > 0 {
		logging.Warn("config sections no extension owns", "sections", extensionReport.Unknown, "extensions", registry.IDs())
	}

	// Opened before the TUI takes the terminal, so a bad address is still
	// something the operator can read. Unset -- the default -- binds nothing.
	profiler, err := debugserver.Start(os.Getenv(debugserver.Env))
	if err != nil {
		return err
	}
	defer profiler.Close()
	if addr := profiler.Addr(); addr != "" {
		logging.Info("pprof listening", "addr", addr)
	}

	// Opened here for the same reason as the profiler: unset binds nothing,
	// and a destination that cannot be reached is worth saying while there
	// is still a terminal to say it on.
	tracer, err := tracing.Start(os.Getenv(tracing.Env))
	if err != nil {
		return err
	}
	defer tracer.Close()
	if where := tracer.Where(); where != "" {
		logging.Info("tracing poll passes", "to", where)
	}

	driver, err := tmux.NewWithSocket(cfg.TmuxSocket)
	if err != nil {
		return err
	}

	// The poll loop holds a control client, and an anchor session for it, on
	// every tmux server it reads. Both are the manager's to take away again.
	defer driver.CloseCaptureClients()

	// So is the window sizing the preview pins. A window left manual stays
	// frozen at the size of a panel that no longer exists, on servers whose
	// windows mostly belong to the operator rather than to the manager.
	defer driver.RestorePinnedWindows()
	// Bubble Tea turns SIGINT and SIGTERM into a quit that reaches those
	// defers; a hangup -- the terminal or the tmux pane the manager runs in
	// closing -- kills the process outright, and nothing else catches it.
	// Set once the dumper exists, so a board hung up on -- which is how a
	// stuck board usually ends -- leaves a dump behind. See dump.go.
	var beforeHangupExit atomic.Pointer[func()]
	defer restoreOnHangup(driver, &beforeHangupExit)()

	engine, err := status.NewEngine(cfg)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// One board per config dir: a second start supersedes the first, which
	// is told to quit (restoring its pins) before this one restores or pins
	// anything, and whose tmux pane is then closed. Two managers over one
	// store and one set of tmux servers is never what an operator meant.
	lock, took, err := singleton.Acquire(dir)
	if err != nil {
		return err
	}
	defer lock.Release()
	if took != nil {
		logging.Info("superseded running manager", "pid", took.PID, "killed", took.Killed, "paneClosed", took.PaneClosed)
	}

	// Install the board's own binary where the sessions it spawns can still
	// find it once this run is gone -- `go run` deletes its build directory
	// on exit, taking every --mcp-config command and $GATE_INBOX_BIN with
	// it. It also refreshes the copy on behalf of sessions an EARLIER run
	// spawned: reconnecting one of their MCP servers re-execs this path, so
	// this is what lets a weeks-old session reach current code.
	installed := launch.Install()
	logging.Info("manager binary installed", "path", installed)
	// The same hooks, in the user's own Claude Code settings, are what a
	// claude started outside the board reports through once it is adopted,
	// and the MCP relay beside them is how it gets the board's tools. The
	// board keeps both in place for as long as it runs.
	// An opencode started outside the board gets the same from a plugin in
	// its global config, kept by the same passes.
	stopClaudeSetup := keepClaudeSetup(dir, cfg.ClaudeCode.SetupOn(), cfg.Codex.SetupOn(), newOpencodeSetup(cfg))
	stopSharedMCP := keepSharedMCP(dir, cfg.ClaudeCode.SharedMCPOn())
	defer stopSharedMCP()
	defer stopClaudeSetup()
	// A board that quits lets go of its adopted panes: their hooks stop
	// reporting rather than writing to rows no board is reading.
	defer func() { _ = hooks.NewManager(dir).SyncAdopted(nil) }()

	// Record which build the board is, so a session whose MCP server was
	// started weeks and many restarts ago can tell that it is answering
	// with an older manager's tools -- the only place that fact is visible
	// at all. A failure here costs a warning, never a startup.
	if err := managerbuild.Record(dir); err != nil {
		logging.Warn("could not record the manager build", "err", err)
	}

	// Hand back anything a previous run pinned and did not live to restore,
	// before this one pins anything of its own. A manager killed with
	// SIGKILL cannot run its own restore, and what that used to leave was a
	// window frozen at the width of a preview panel belonging to a process
	// that no longer exists -- with nothing but the tmux incantation to get
	// it back. Restoring first also keeps this run's own record honest: a
	// window still pinned when it is measured would record the dead
	// manager's "manual" as the value the operator had.
	journal := filepath.Join(dir, "pins.json")
	driver.RestoreJournaledPins(journal)
	driver.SetPinJournal(journal)
	// An earlier run, or a pane split beside this one, can have left the
	// board's own window pinned, which keeps it at some old terminal's size
	// whatever the operator attaches from.
	driver.UnpinOwnWindow()

	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	stopUsage := accounts.StartUsageMonitor(st, cfg.Tools, driver.Exists)
	defer stopUsage()

	logging.Info("startup",
		"version", version,
		"home", dir,
		"store", filepath.Join(dir, "state.db"),
		"log", logger.Path(),
		// Not "level": slog writes the record's own severity under that key.
		"logLevel", logging.LevelName(logger.Level()),
		"socket", driver.SocketName(),
		"pollInterval", cfg.PollInterval.Duration.String(),
		"nameSweepPace", cfg.NameSweepPace.Duration.String(),
		"adoptSockets", cfg.AdoptSockets,
		"tools", sortedToolNames(cfg),
		"extensions", registry.IDs(),
		"editor", cfg.Editor)

	logDir := filepath.Join(dir, logging.DirName)
	if path := logger.Path(); path != "" {
		logDir = filepath.Dir(path)
	}
	// While the board is drawn, anything written to file descriptor 2 lands
	// on top of the frame. On macOS that includes libmalloc's
	// "MallocStackLogging: can't turn off ..." line, which every process
	// prints from inside the system library when memory pressure turns
	// critical, so no call site of ours can suppress it. Point fd 2 at a file
	// for the board's lifetime instead.
	restoreStderr := redirectStderr(logDir)
	defer restoreStderr()

	model := ui.New(cfg, st, driver, engine, hooks.NewManager(dir), version)
	model.SetExtensionNotes(extensionReport.Notes())
	// Mouse reporting claims the wheel for the app, so a notch neither
	// scrolls the host's scrollback out from under the manager nor arrives
	// as an arrow key that walks the session cursor. Alternate scroll is
	// cleared too: a crashed earlier run can leave it set.
	program := tea.NewProgram(model)
	if err := ui.DisableAlternateScroll(); err != nil {
		return err
	}
	// Put the terminal's background where the frame expects it — through
	// tmux's passthrough envelope when a multiplexer is hosting us. Which
	// colour that is depends on the backdrop mode: inheriting, it is the
	// one the terminal already had, and this first write changes nothing.
	ui.EnableTerminalPassthrough()
	ui.SyncTerminalBackground()
	stopExtensions, err := startExtensions(dir, registry, model, program.Send)
	if err != nil {
		return err
	}
	model.StartPoller(program.Send)
	// `gate-inbox dump` (SIGUSR1), a hangup, a run of refused opens and a
	// wedged event loop each write the input journal and goroutine stacks
	// next to the log; see dump.go.
	dumps, stopDumps := startDumper(logDir, program.Send)
	defer stopDumps()
	model.SetAutoDump(dumps.Trigger)
	onHangup := dumps.OnHangup
	beforeHangupExit.Store(&onHangup)
	_, runErr := program.Run()
	stopExtensions()
	ui.ResetTerminalBackground()
	logging.Info("shutdown", logging.Err(runErr),
		"droppedLogLines", logger.Dropped(), "droppedTraces", tracing.Dropped())
	return runErr
}

// startExtensions installs every UIProvider's keys and badges on the model,
// then starts every BoardProvider the build carries against the board model
// polls, and returns what stops them again. It runs before the first pass, so
// no transition goes unseen by a subscriber made at start, and a provider can
// badge a row from its first event.
func startExtensions(dir string, registry *extension.Registry, model *ui.Model, send func(tea.Msg)) (func(), error) {
	cmds := sessioncmd.NewSessions(dir, sessioncmd.MCPVocabulary())
	// A board killed mid-hold ran no extension stop, so its holds are still
	// on record with nothing left to settle them. Aborted before any
	// extension starts, so none can mistake one for its own.
	aborted, err := cmds.AbortOrphanedHolds()
	if err != nil {
		logging.Warn("could not abort every replacement an earlier board held", logging.Err(err))
	}
	if aborted > 0 {
		logging.Info("aborted replacements an earlier board held", "count", aborted)
	}
	model.UseQuestionTyper(cmds.BoardType)
	board := extensionhost.NewBoard(dir, cmds)
	events := extensionhost.NewEvents(board, func(owner string, err error) {
		logging.Warn("extension board subscriber", "extension", owner, logging.Err(err))
	})
	model.ObserveBoard(events)
	events.OnPinChange(model.PinStatuses(events))
	model.UseQueueDeadlines(events)
	ctx, cancel := context.WithCancel(context.Background())
	go expireGrants(ctx, cmds, grantSweep)
	go restartStaleSandboxes(ctx, cmds, staleSandboxSweep)
	go unparkInFlight(ctx, cmds, hostBootedAt, unparkOnStartDelay)
	if err := startUI(ctx, registry, model, send); err != nil {
		cancel()
		return nil, err
	}
	results, stop, err := registry.StartBoard(ctx, events.For)
	if err != nil {
		cancel()
		return nil, err
	}
	for _, result := range results {
		if result.Err != nil {
			logging.Warn("extension did not start on the board",
				"extension", result.ID, "version", result.Version, logging.Err(result.Err))
			continue
		}
		logging.Info("extension started on the board", "extension", result.ID, "version", result.Version)
	}
	return func() {
		cancel()
		if err := stop(); err != nil {
			logging.Warn("extension did not stop cleanly", logging.Err(err))
		}
	}, nil
}

// grantSweep is how often the board looks for a permission grant whose window
// has closed. A grant outlives its window by up to this much, and by however
// long its session's turn runs on: see ExpireGrants.
const grantSweep = 30 * time.Second

// expireGrants revokes lapsed permission grants until ctx ends. It runs on
// the board because the board is the one process that is always up while
// sessions are; a grant made while no board runs lapses when one starts.
func expireGrants(ctx context.Context, cmds *sessioncmd.Sessions, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		revoked, err := cmds.ExpireGrants(time.Now())
		if err != nil {
			logging.Warn("could not expire every lapsed permission grant", logging.Err(err))
		}
		if revoked > 0 {
			logging.Info("expired permission grants", "count", revoked)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// staleSandboxSweep is how often the board looks for a child whose sandbox
// went stale and whose turn has ended. The child is idle and waiting for it,
// so this is short.
const staleSandboxSweep = 5 * time.Second

// restartStaleSandboxes restarts children whose sandbox stayed stale until ctx
// ends; see sessioncmd.RestartStaleSandboxes.
func restartStaleSandboxes(ctx context.Context, cmds *sessioncmd.Sessions, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		restarted, err := cmds.RestartStaleSandboxes(time.Now())
		if err != nil {
			logging.Warn("could not restart every session with a stale sandbox", logging.Err(err))
		}
		if restarted > 0 {
			logging.Info("restarted sessions with a stale sandbox", "count", restarted)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// unparkOnStartDelay is how long a starting board waits before bringing back
// what park left in flight: long enough for a machine that has just booted
// to finish starting its own services, and for the board's first poll and
// adoption to settle, so the first relaunch lands on a quiet host.
const unparkOnStartDelay = 20 * time.Second

// unparkSource is what unparkInFlight reads and calls, narrowed so a test
// can stand in for the store and the relaunches.
type unparkSource interface {
	ParkedAt() (time.Time, error)
	Unpark(sessionID string) (sessioncmd.UnparkResult, error)
}

// unparkInFlight brings back, once, every parked session when a board starts
// on a host that has booted since the park: the ones with work in flight and
// the sessions tracking them first, then the rest with nothing sent. Park is
// run before a reboot, so a board starting after one is the reboot done, and
// the fan-outs that were mid-flight carry on without anyone typing unpark. A
// board restarted before the reboot leaves them parked: bringing them back
// then only hands them to the reboot, with the set already cleared of them.
//
// Every relaunch takes its turn in the shared queue, so this can run for the
// best part of an hour on a large set; it holds nothing the board waits on.
func unparkInFlight(ctx context.Context, src unparkSource, bootedAt func() (time.Time, error), delay time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	parkedAt, err := src.ParkedAt()
	if err != nil || parkedAt.IsZero() {
		return
	}
	booted, err := bootedAt()
	if err != nil {
		logging.Warn("left parked sessions parked: the host boot time is unreadable", logging.Err(err))
		return
	}
	if !booted.After(parkedAt) {
		logging.Info("left parked sessions parked: the host has not rebooted since the park", "parkedAt", parkedAt, "bootedAt", booted)
		return
	}
	result, err := src.Unpark("")
	if err != nil {
		logging.Warn("could not bring back the parked sessions", logging.Err(err))
		return
	}
	if len(result.Revived) > 0 || len(result.Errors) > 0 {
		logging.Info("brought back parked sessions",
			"revived", len(result.Revived), "nudged", len(result.Nudged),
			"failed", len(result.Errors), "owed", result.Owed)
	}
	for _, failure := range result.Errors {
		logging.Warn("parked session not brought back", "detail", failure)
	}
}

// hostBootedAt is when the host last booted.
func hostBootedAt() (time.Time, error) {
	secs, err := host.BootTime()
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(int64(secs), 0), nil
}

// exitHangup is the conventional status for a process ending on SIGHUP,
// which is what this one is standing in for.
const exitHangup = 128 + int(syscall.SIGHUP)

// restoreOnHangup unpins the operator's windows when the manager is hung up
// on, and returns the func that takes the handler down again.
//
// A hangup is the terminal or the tmux pane the manager is running in being
// closed, and its default action kills the process before any defer runs.
// The exit below is abrupt in exchange -- the store is left unclosed, which
// an uncaught hangup would have done too -- and a window left pinned outlives
// the manager for as long as tmux keeps running, which nothing undoes.
//
// SIGKILL is out of reach from in here by construction. A manager killed with
// it leaves its pins behind, and the next run does not know they were its.
//
// beforeExit, when set, runs first: it is how a dump is left behind.
func restoreOnHangup(driver *tmux.Driver, beforeExit *atomic.Pointer[func()]) func() {
	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-hangup:
			// Logged, and the log closed, because the exit below skips
			// every defer: until this line a hung-up board left no trace.
			logging.Info("hung up; exiting")
			if run := beforeExit.Load(); run != nil {
				(*run)()
			}
			driver.RestorePinnedWindows()
			_ = logging.Default().Close()
			os.Exit(exitHangup)
		}
	}()
	return func() {
		signal.Stop(hangup)
		close(done)
	}
}

// openLog swallows every failure on purpose: the caller is about to take
// over the terminal, so there is nowhere to report one, and running without a
// log beats not running.
func openLog(cfg config.Config) *logging.Logger {
	dir, err := config.Dir()
	if err != nil {
		return nil
	}
	opts, err := logging.Resolve(dir, logSettings(cfg))
	if err != nil {
		// An unparseable level is not worth refusing to log over.
		opts.Level = logging.LevelInfo
	}
	logger, err := logging.Open(opts)
	if err != nil {
		return nil
	}
	return logger
}

func sortedToolNames(cfg config.Config) []string {
	names := cfg.ToolNames()
	sort.Strings(names)
	return names
}
