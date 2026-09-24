// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"

	tea "charm.land/bubbletea/v2"
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
	// Refused here, before the singleton lock, so a config the build's
	// extensions reject leaves a running board where it is rather than
	// evicting it for a board that cannot start.
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	if err := registry.Configure(dir, cfg.Extensions); err != nil {
		return err
	}

	// The log is opened before anything else can fail, because the whole
	// point of it is to have a record of the failures. A log that cannot be
	// opened is not a reason to refuse to start: the manager runs without it.
	logger := openLog(cfg)
	defer logger.Close()
	logging.SetDefault(logger)

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
	defer restoreOnHangup(driver)()

	engine, err := status.NewEngine(cfg)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// One board per config dir: a second start supersedes the first, which
	// is told to quit (restoring its pins) before this one restores or pins
	// anything. Two managers over one store and one set of tmux servers is
	// never what an operator meant.
	lock, took, err := singleton.Acquire(dir)
	if err != nil {
		return err
	}
	defer lock.Release()
	if took != nil {
		logging.Info("superseded running manager", "pid", took.PID, "killed", took.Killed)
	}

	// Install the board's own binary where the sessions it spawns can still
	// find it once this run is gone -- `go run` deletes its build directory
	// on exit, taking every --mcp-config command and $GATE_INBOX_BIN with
	// it. It also refreshes the copy on behalf of sessions an EARLIER run
	// spawned: reconnecting one of their MCP servers re-execs this path, so
	// this is what lets a weeks-old session reach current code.
	installed := launch.Install()
	logging.Info("manager binary installed", "path", installed)

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

	model := ui.New(cfg, st, driver, engine, hooks.NewManager(dir), version)
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
	board := extensionhost.NewBoard(dir, sessioncmd.NewSessions(dir, sessioncmd.MCPVocabulary()))
	events := extensionhost.NewEvents(board, func(owner string, err error) {
		logging.Warn("extension board subscriber", "extension", owner, logging.Err(err))
	})
	model.ObserveBoard(events)
	ctx, cancel := context.WithCancel(context.Background())
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
func restoreOnHangup(driver *tmux.Driver) func() {
	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-hangup:
			driver.RestorePinnedWindows()
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
