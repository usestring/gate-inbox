package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/usestring/gate-inbox/internal/cli"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcprelay"
)

const claudeHooksUsage = "claude-hooks [install | uninstall [--all] | status]"

// claudeHooksHelp lists the command beside the session commands.
var claudeHooksHelp = cli.HelpSection{Title: "Setup", Commands: []cli.HelpEntry{{
	Usage: claudeHooksUsage,
	About: "register Gate Inbox's hooks and MCP relay in your Claude Code user config, so a claude started outside the board works fully once adopted; the board installs them at startup",
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
	registerGlobalHooksAt(path, dir, bin)
	registerRelay(dir, bin)
}

// registerRelay puts the MCP relay in the user's Claude Code config, so an
// adopted claude gets the board's tools. Like the hooks, it never fails the
// board, and it costs nothing when the config already carries it: the claude
// CLI runs only to change the entry.
func registerRelay(dir, bin string) {
	path, err := mcprelay.ClaudeStatePath()
	if err != nil {
		logging.Warn("gate-inbox MCP relay not registered", logging.Err(err))
		return
	}
	if state, err := mcprelay.Lookup(path, dir, bin); err == nil && state == mcprelay.Current {
		return
	}
	claude, err := claudeCLI()
	if err != nil {
		logging.Info("gate-inbox MCP relay left off", "reason", err.Error())
		return
	}
	changed, err := mcprelay.Register(claude, path, dir, bin)
	if err != nil {
		logging.Warn("gate-inbox MCP relay not registered", "config", path, logging.Err(err))
		return
	}
	logging.Info("gate-inbox MCP relay registered", "config", path, "changed", changed)
}

// claudeCLI runs the claude on PATH, for the MCP commands that edit its user
// config.
func claudeCLI() (mcprelay.Claude, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, errors.New("no claude on PATH")
	}
	return func(args ...string) error {
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("claude %s: %w: %s", strings.Join(args[:2], " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}, nil
}

// relayChange registers or removes the relay for the claude-hooks command,
// and says what it did.
func relayChange(install bool, configDir, bin string, all bool) (string, error) {
	path, err := mcprelay.ClaudeStatePath()
	if err != nil {
		return "", err
	}
	claude, err := claudeCLI()
	if err != nil {
		return "left the Gate Inbox MCP relay alone: " + err.Error(), nil
	}
	if install {
		changed, err := mcprelay.Register(claude, path, configDir, bin)
		if err != nil {
			return "", err
		}
		return describeChange(changed, "registered the Gate Inbox MCP relay in "+path, path+" already carries the Gate Inbox MCP relay"), nil
	}
	changed, err := mcprelay.Unregister(claude, path, configDir, all)
	if err != nil {
		return "", err
	}
	return describeChange(changed, "removed the Gate Inbox MCP relay from "+path, path+" carries no Gate Inbox MCP relay"), nil
}

func relayStatus(configDir, bin string) string {
	path, err := mcprelay.ClaudeStatePath()
	if err != nil {
		return err.Error()
	}
	state, err := mcprelay.Lookup(path, configDir, bin)
	if err != nil {
		return path + ": " + err.Error()
	}
	words := map[mcprelay.State]string{
		mcprelay.Absent:     "MCP relay not registered",
		mcprelay.Current:    "MCP relay registered",
		mcprelay.Stale:      "MCP relay registered by another build",
		mcprelay.OtherBoard: "MCP relay registered by another board",
		mcprelay.Foreign:    "an MCP server named gate-inbox that Gate Inbox did not write",
	}
	return path + ": " + words[state]
}

// registerGlobalHooksAt is the write itself. A settings file that is
// malformed, read-only or otherwise unwritable is left exactly as it was, and
// the board starts anyway.
func registerGlobalHooksAt(path, dir, bin string) {
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
		if _, err := fmt.Fprintln(out, describeChange(changed, "registered Gate Inbox's hooks in "+path, path+" already carries Gate Inbox's hooks")); err != nil {
			return err
		}
		said, err := relayChange(true, configDir, bin, false)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, said)
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
		if _, err := fmt.Fprintln(out, describeChange(changed, "removed Gate Inbox's hooks from "+path, path+" carries none of Gate Inbox's hooks")); err != nil {
			return err
		}
		said, err := relayChange(false, configDir, "", scope == "")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, said)
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
		_, err = fmt.Fprintf(out, "%s: %s\n%s\n", path, state, relayStatus(configDir, bin))
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
