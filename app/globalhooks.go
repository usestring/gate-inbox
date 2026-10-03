package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/usestring/gate-inbox/internal/cli"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
)

const claudeHooksUsage = "claude-hooks [install | uninstall [--all] | status]"

// claudeHooksHelp lists the command beside the session commands.
var claudeHooksHelp = cli.HelpSection{Title: "Setup", Commands: []cli.HelpEntry{{
	Usage: claudeHooksUsage,
	About: "register Gate Inbox's hooks in your Claude Code user settings, so a claude started outside the board works fully once adopted; the board installs them at startup",
}}}

// registerGlobalHooks puts the global hooks in the user's Claude Code settings
// at board startup (hooks.RegisterGlobal). It never fails the board: a file it
// cannot read or write costs adopted sessions their hooks, and a warning.
//
// It stands down for a board whose home is a scratch directory, which is what
// a test or a trial run uses: those homes vanish, and a user's settings must
// not be left naming them. `claude-hooks install` still registers one by hand.
func registerGlobalHooks(dir string) {
	manager := hooks.NewManager(dir)
	if manager.GlobalDisabled() {
		logging.Info("global claude hooks left off", "reason", "removed by the operator")
		return
	}
	if underTempDir(dir) {
		logging.Info("global claude hooks left off", "reason", "scratch home", "home", dir)
		return
	}
	bin := launch.Installed()
	if bin == "" {
		logging.Info("global claude hooks left off", "reason", "no installed binary")
		return
	}
	path, err := hooks.GlobalSettingsPath()
	if err != nil {
		logging.Warn("global claude hooks not registered", logging.Err(err))
		return
	}
	changed, err := hooks.RegisterGlobal(path, dir, bin)
	if err != nil {
		logging.Warn("global claude hooks not registered", "settings", path, logging.Err(err))
		return
	}
	logging.Info("global claude hooks registered", "settings", path, "changed", changed)
}

func underTempDir(dir string) bool {
	temp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		temp = os.TempDir()
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	rel, err := filepath.Rel(temp, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// runClaudeHooks is the claude-hooks command. uninstall also keeps the board
// from putting them back; install clears that.
func runClaudeHooks(out io.Writer, args []string, configDir string) error {
	verb := "status"
	if len(args) > 0 {
		verb = args[0]
	}
	if verb == "-h" || verb == "--help" {
		_, err := fmt.Fprintf(out, "usage: %s %s\n", Name, claudeHooksUsage)
		return err
	}
	path, err := hooks.GlobalSettingsPath()
	if err != nil {
		return err
	}
	manager := hooks.NewManager(configDir)
	switch {
	case verb == "install" && len(args) == 1:
		bin := launch.Installed()
		if bin == "" {
			bin = launch.Executable()
		}
		if err := manager.SetGlobalDisabled(false); err != nil {
			return err
		}
		changed, err := hooks.RegisterGlobal(path, configDir, bin)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, describeChange(changed, "registered Gate Inbox's hooks in "+path, path+" already carries Gate Inbox's hooks"))
		return err
	case verb == "uninstall" && (len(args) == 1 || len(args) == 2 && args[1] == "--all"):
		scope := configDir
		if len(args) == 2 {
			scope = ""
		}
		changed, err := hooks.UnregisterGlobal(path, scope)
		if err != nil {
			return err
		}
		if err := manager.SetGlobalDisabled(true); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, describeChange(changed, "removed Gate Inbox's hooks from "+path, path+" carries none of Gate Inbox's hooks"))
		return err
	case verb == "status" && len(args) <= 1:
		bin := launch.Installed()
		if bin == "" {
			bin = launch.Executable()
		}
		registered, err := hooks.GlobalRegistered(path, configDir, bin)
		if err != nil {
			return err
		}
		state := "not registered"
		if registered {
			state = "registered"
		}
		if manager.GlobalDisabled() {
			state += " (the board will not register them; run `" + Name + " claude-hooks install`)"
		}
		_, err = fmt.Fprintf(out, "%s: %s\n", path, state)
		return err
	}
	return fmt.Errorf("usage: %s %s", Name, claudeHooksUsage)
}

func describeChange(changed bool, did, already string) string {
	if changed {
		return did
	}
	return already
}
