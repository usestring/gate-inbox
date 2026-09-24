// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package app is the board as a library: the composition root every
// entry point of the executable goes through. A module that wants its own
// build of the board -- with extensions of its own compiled in -- writes a
// main that calls Run, and gets the same TUI, CLI and per-session MCP server
// this module's own command does.
//
// The executable is one program with three faces. With no arguments it is
// the interactive board; "mcp" is the MCP server every session it spawns
// is registered with, re-executing this same binary; anything else is a CLI
// command a session runs from its own shell. Run serves all three from one
// Options, so the extensions a build carries are the same set whichever
// face is asked.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/cli"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcpserver"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// Options is what a build of the board is composed from.
type Options struct {
	// Extensions are the optional features compiled into this build, in
	// the order their tools are registered. The slice is the whole set:
	// nothing registers itself.
	Extensions []extension.Extension
	// BuildInfo describes the executable.
	BuildInfo BuildInfo
	// ConfigDefaults is config.toml text laid under the operator's own file
	// on every load: a key the file defines wins, even defined empty; a key
	// only these define comes from here; anything still unset takes the
	// built-in default. Tables merge key by key, and any other value, an
	// array included, is replaced whole. It is how a distribution fills in
	// what DistributionSupplied lists without rewriting anybody's file. Run
	// refuses text that does not parse or names a key the board does not
	// read.
	ConfigDefaults string
}

// Name is the command this program is run as.
const Name = "gate-inbox"

// BuildInfo describes the executable Run is serving.
type BuildInfo struct {
	// Version is the release this build reports, usually stamped in with
	// -ldflags. Empty or "dev" falls back to the module version a
	// `go install` at a tag records, and to "dev" when there is none.
	Version string
}

// ErrUnknownCommand is what Run returns for a verb it does not know.
var ErrUnknownCommand = errors.New("unknown command")

// ExitCode is the process status a main should exit with for what Run
// returned: 0 for nil, 2 for a mistyped command, 1 for anything else.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUnknownCommand):
		return 2
	default:
		return 1
	}
}

// Run is the whole program: args are the command-line arguments after the
// program name. It prints its own output; the error it returns is for the
// caller to print and to turn into an exit status with ExitCode.
func Run(ctx context.Context, args []string, opts Options) error {
	info, hasInfo := debug.ReadBuildInfo()
	version := resolveVersion(opts.BuildInfo.Version, info, hasInfo)
	// Traces report the tag only when there is one. resolveVersion's "dev"
	// is the absence of a release, and a dataset cannot tell a placeholder
	// from a version somebody shipped.
	if version != devVersion {
		tracing.Release = version
	}

	// A build whose extension set is malformed -- two with one ID, one with
	// no ID -- or whose defaults configure an extension it does not carry is
	// broken whichever face is asked, and says so before any of them does
	// anything.
	registry, err := extension.NewRegistry(opts.Extensions)
	if err != nil {
		return err
	}
	if _, err := config.UseDefaults(opts.ConfigDefaults); err != nil {
		return err
	}
	if err := defaultsOwnedBy(registry); err != nil {
		return err
	}
	accounts.UsePool(poolOf(registry))

	if len(args) == 0 {
		return runBoard(version, registry)
	}
	switch args[0] {
	case "help", "--help", "-h":
		return printHelp(os.Stdout)
	case "--version", "-v":
		fmt.Println(Name, version)
		return nil
	case "--log-path", "logs":
		return printLogPath(os.Stdout)
	}
	command, ok := subcommands(ctx, version, opts.Extensions)[args[0]]
	if !ok {
		// An unknown verb is a mistyped command, never a request for the
		// board: falling through to the board from a session's shell
		// evicted the operator's running board.
		return unknownCommand(args[0])
	}
	if err := command(args[1:]); err != nil {
		// A subcommand's -h has already printed its usage, and asking for
		// it is not a failure.
		if errors.Is(err, cli.ErrUsageShown) {
			return nil
		}
		return err
	}
	return nil
}

// poolOf finds the build's account pool the first time a launch needs one.
// The board has configured every extension by then; a CLI command or the
// MCP server has not, and configures only the one supplying the pool.
func poolOf(registry *extension.Registry) func() (extension.AccountPool, error) {
	return sync.OnceValues(func() (extension.AccountPool, error) {
		if registry.Configured() {
			return registry.AccountPool("", nil)
		}
		dir, err := config.Dir()
		if err != nil {
			return nil, err
		}
		cfg, err := config.Load()
		if err != nil {
			return nil, err
		}
		return registry.AccountPool(dir, cfg.Extensions)
	})
}

// defaultsOwnedBy refuses defaults with an [extensions.<id>] section no
// extension in registry owns. An operator's file with one is refused when a
// face configures its extensions; the defaults ship with the build, so the
// same mistake there is refused up front, on every face.
func defaultsOwnedBy(registry *extension.Registry) error {
	ids := registry.IDs()
	var unowned []string
	for _, id := range config.DefaultExtensionSections() {
		if !slices.Contains(ids, id) {
			unowned = append(unowned, id)
		}
	}
	if len(unowned) == 0 {
		return nil
	}
	have := strings.Join(ids, ", ")
	if have == "" {
		have = "none"
	}
	return fmt.Errorf("config defaults: [extensions] has section(s) no extension in this build owns: %s (this build has: %s)",
		strings.Join(unowned, ", "), have)
}

func unknownCommand(arg string) error {
	return fmt.Errorf("%w %q; run `%s help` for the list", ErrUnknownCommand, arg, Name)
}

func printHelp(w io.Writer) error {
	_, err := fmt.Fprintln(w, cli.Help())
	return err
}

// printLogPath answers "where do I find the log" without making the user
// guess at a config directory. It runs before the TUI does, so writing to
// stdout here is safe -- and it is the only path in this program that may.
func printLogPath(w io.Writer) error {
	opts, err := logOptions()
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "log file:  ", opts.Path)
	fmt.Fprintln(w, "log level: ", logging.LevelName(opts.Level))
	fmt.Fprintf(w, "rotation:   %d MB per file, %d kept, %d MB total, compressed=%v\n",
		opts.MaxSizeMB, opts.MaxBackups, opts.MaxTotalMB, opts.Compress)
	entries, err := os.ReadDir(filepath.Dir(opts.Path))
	if err != nil {
		fmt.Fprintln(w, "no log files yet")
		return nil
	}
	fmt.Fprintln(w, "files:")
	found := false
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || entry.IsDir() || !logging.Owns(opts.Path, entry.Name()) {
			continue
		}
		found = true
		fmt.Fprintf(w, "  %-40s %8d bytes  %s\n", entry.Name(), info.Size(),
			info.ModTime().Format("2006-01-02 15:04:05"))
	}
	if !found {
		fmt.Fprintln(w, "  (none yet)")
	}
	return nil
}

// logOptions falls back to the built-in defaults on a config that will not
// parse: a broken config must not also be what stops somebody finding their
// log.
func logOptions() (logging.Options, error) {
	dir, err := config.Dir()
	if err != nil {
		return logging.Options{}, err
	}
	var settings logging.Settings
	if cfg, err := config.Load(); err == nil {
		settings = logSettings(cfg)
	}
	return logging.Resolve(dir, settings)
}

func logSettings(cfg config.Config) logging.Settings {
	return logging.Settings{
		Level:      cfg.Log.Level,
		File:       cfg.Log.File,
		MaxSizeMB:  cfg.Log.MaxSizeMB,
		MaxBackups: cfg.Log.MaxBackups,
		MaxTotalMB: cfg.Log.MaxTotalMB,
		NoCompress: cfg.Log.NoCompress,
	}
}

// subcommands is every verb other than the board itself. "mcp" is the one
// extensions reach today: it is handed the build's set and registers the
// tools of whichever ones the operator's config switches on.
func subcommands(ctx context.Context, version string, extensions []extension.Extension) map[string]func(args []string) error {
	table := map[string]func(args []string) error{
		"mcp": withConfigDir(func(args []string, sessionID, configDir string) error {
			return mcpserver.Run(ctx, configDir, sessionID, version, extensions)
		}),
	}
	for name, command := range cli.Commands() {
		if name != "spawn" && name != "migrate" && name != "revive" && name != "unpark" {
			table[name] = withConfigDir(command)
			continue
		}
		table[name] = withConfigDir(func(args []string, sessionID, configDir string) error {
			tracer, err := tracing.Start(os.Getenv(tracing.Env))
			if err != nil {
				return err
			}
			defer tracer.Close()
			return command(args, sessionID, configDir)
		})
	}
	return table
}

func withConfigDir(command func(args []string, sessionID, configDir string) error) func([]string) error {
	return func(args []string) error {
		dir, err := config.Dir()
		if err != nil {
			return err
		}
		return command(args, envname.Get(hooks.EnvSessionID), dir)
	}
}
