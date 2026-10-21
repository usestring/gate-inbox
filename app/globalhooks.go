package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcprelay"
)

const claudeHooksUsage = "claude-hooks [install | uninstall [--all] | status]"

// claudeSetupEvery is how often a running board checks that its Claude Code
// entries are still in place. A check that finds them reads two files and
// writes nothing, so this only bounds how long an entry deleted by hand, or
// left naming a binary that has since moved, goes unrepaired.
const claudeSetupEvery = 5 * time.Minute

// claudeSwitchEvery is how often it looks at the Settings switch, one stat,
// so switching the setup off there takes the entries out within seconds.
const claudeSwitchEvery = 5 * time.Second

// claudeSetup keeps Gate Inbox's hooks and MCP relay in the user's Claude
// Code config for as long as the board runs, or keeps them out when the
// operator switched them off. Nobody runs a command for it: opening the board
// is the setup. It never fails the board: a file it cannot read or write
// costs adopted sessions their hooks or tools, and one warning.
type claudeSetup struct {
	dir string
	// configOn is config.toml's [claude_code] setup, read at startup.
	configOn bool
	// codexOn is config.toml's [codex] setup, read at startup.
	codexOn bool
	bin     func() string
	// codexInstalled reports whether a codex is on PATH: the codex hooks
	// are registered only on a machine that has one.
	codexInstalled func() bool
	// applied and codexApplied are what the last pass did, so the switch
	// ticker only syncs on a change.
	applied      bool
	codexApplied bool
	// warned holds the last warning per entry, so a settings file that stays
	// malformed is reported once rather than every few minutes.
	warned map[string]string
}

func newClaudeSetup(dir string, configOn, codexOn bool) *claudeSetup {
	return &claudeSetup{
		dir: dir, configOn: configOn, codexOn: codexOn, bin: launch.Installed,
		codexInstalled: codexOnPath, warned: map[string]string{},
	}
}

func codexOnPath() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

// keepClaudeSetup keeps the Claude Code entries and the Codex hooks
// (codexOn, from config.toml's [codex] setup) in step with the board. It
// syncs them once before the board draws anything,
// then again every claudeSetupEvery, and whenever the Settings switch flips,
// until stop is called.
//
// It stands down for a board whose home is a scratch directory, which is what
// a test or a trial run uses: those homes vanish, and a user's settings must
// not be left naming them.
func keepClaudeSetup(dir string, configOn, codexOn bool) (stop func()) {
	if underTempDir(dir) {
		logging.Info("claude code setup left off", "reason", "scratch home", "home", dir)
		return func() {}
	}
	setup := newClaudeSetup(dir, configOn, codexOn)
	setup.sync()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		setup.run(ctx, claudeSetupEvery, claudeSwitchEvery)
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *claudeSetup) run(ctx context.Context, every, switchEvery time.Duration) {
	full, flip := time.NewTicker(every), time.NewTicker(switchEvery)
	defer full.Stop()
	defer flip.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-full.C:
			s.sync()
		case <-flip.C:
			if s.wanted() != s.applied || s.codexWanted() != s.codexApplied {
				s.sync()
			}
		}
	}
}

// wanted is the operator's choice: config.toml and the Settings switch must
// both leave the setup on. The switch is the hooks/global-hooks.disabled file
// `claude-hooks uninstall` has always written, so a board that was opted out
// that way stays opted out.
func (s *claudeSetup) wanted() bool {
	return s.configOn && !hooks.NewManager(s.dir).GlobalDisabled()
}

// sync makes the user's Claude Code config match wanted. Each half is a read
// that changes nothing when the entries are already right; only a difference
// writes the settings file or runs the claude CLI.
func (s *claudeSetup) sync() {
	s.syncCodex()
	want := s.wanted()
	s.applied = want
	settings, settingsErr := hooks.GlobalSettingsPath()
	state, stateErr := mcprelay.ClaudeStatePath()
	if !want {
		if settingsErr == nil {
			changed, err := hooks.UnregisterGlobal(settings, s.dir)
			s.report("hooks", "global claude hooks removed", "settings", settings, changed, err)
		}
		if stateErr == nil {
			changed, err := mcprelay.Unregister(claudeOnPath, state, s.dir, false)
			s.report("relay", "gate-inbox MCP relay removed", "config", state, changed, err)
		}
		return
	}
	bin := s.bin()
	if bin == "" {
		s.report("hooks", "", "", "", false, errors.New("no installed binary"))
		return
	}
	if settingsErr != nil {
		s.report("hooks", "", "", "", false, settingsErr)
	} else {
		changed, err := hooks.RegisterGlobal(settings, s.dir, bin)
		s.report("hooks", "global claude hooks registered", "settings", settings, changed, err)
	}
	if stateErr != nil {
		s.report("relay", "", "", "", false, stateErr)
	} else {
		changed, err := mcprelay.Register(claudeOnPath, state, s.dir, bin)
		s.report("relay", "gate-inbox MCP relay registered", "config", state, changed, err)
	}
}

// codexWanted is the operator's choice for the codex hooks: config.toml's
// [codex] setup and the codex-hooks.disabled file `codex-hooks uninstall`
// writes must both leave them on.
func (s *claudeSetup) codexWanted() bool {
	return s.codexOn && !hooks.NewManager(s.dir).CodexDisabled()
}

// syncCodex makes the user's codex config.toml match codexWanted: this
// board's two hook entries in place, or taken out. A machine with no codex
// gets nothing written; one that already has the entries keeps them as they
// are when they match, so codex's trust in them stands.
func (s *claudeSetup) syncCodex() {
	want := s.codexWanted()
	s.codexApplied = want
	path, err := hooks.CodexConfigPath()
	if err != nil {
		s.report("codex", "", "", "", false, err)
		return
	}
	if !want {
		changed, err := hooks.UnregisterCodex(path, s.dir)
		s.report("codex", "global codex hooks removed", "config", path, changed, err)
		return
	}
	if s.codexInstalled == nil || !s.codexInstalled() {
		s.report("codex", "", "", "", false, errNoCodex)
		return
	}
	bin := s.bin()
	if bin == "" {
		s.report("codex", "", "", "", false, errors.New("no installed binary"))
		return
	}
	changed, err := hooks.RegisterCodex(path, s.dir, bin)
	s.report("codex", "global codex hooks registered", "config", path, changed, err)
}

var errNoCodex = errors.New("no codex on PATH")

// report logs a change once and a failure once per distinct message. A
// machine with no claude on it is not a fault, so that one is info.
func (s *claudeSetup) report(entry, did, key, path string, changed bool, err error) {
	if err == nil {
		delete(s.warned, entry)
		if changed {
			logging.Info(did, key, path)
		}
		return
	}
	if s.warned[entry] == err.Error() {
		return
	}
	s.warned[entry] = err.Error()
	if errors.Is(err, errNoClaude) || errors.Is(err, errNoCodex) {
		logging.Info("claude code setup: "+entry+" left off", "reason", err.Error())
		return
	}
	logging.Warn("claude code setup: "+entry+" not in place", logging.Err(err))
}

var errNoClaude = errors.New("no claude on PATH")

// claudeOnPath runs the claude found on PATH at the moment it is needed, so a
// board started before claude was installed picks it up on a later pass, and
// a pass with nothing to change never looks.
func claudeOnPath(args ...string) error {
	claude, err := claudeCLI()
	if err != nil {
		return err
	}
	return claude(args...)
}

// claudeCLI runs the claude on PATH, for the MCP commands that edit its user
// config.
func claudeCLI() (mcprelay.Claude, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, errNoClaude
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

// runClaudeHooks is the claude-hooks command, a maintenance tool the help
// leaves out: the board does this itself. uninstall flips the same switch as
// Settings, so the board keeps them out; install flips it back.
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
		switch {
		case !configSetupOn(configDir):
			state += " (switched off by [claude_code] setup = false in config.toml; the board removes them)"
		case manager.GlobalDisabled():
			state += " (claude code setup is switched off in Settings; the board removes them)"
		}
		_, err = fmt.Fprintf(out, "%s: %s\n%s\n", path, state, relayStatus(configDir, bin))
		return err
	}
	return fmt.Errorf("usage: %s %s", Name, claudeHooksUsage)
}

// configSetupOn reads [claude_code] setup from the board's config, without
// writing a default config where there is none.
func configSetupOn(configDir string) bool {
	if _, err := os.Stat(filepath.Join(configDir, "config.toml")); err != nil {
		return true
	}
	cfg, err := config.LoadDir(configDir)
	return err != nil || cfg.ClaudeCode.SetupOn()
}

func describeChange(changed bool, did, already string) string {
	if changed {
		return did
	}
	return already
}

const codexHooksUsage = "codex-hooks [install | uninstall [--all] | status]"

// runCodexHooks is the codex-hooks command, the codex counterpart of
// claude-hooks: a maintenance tool the help leaves out, since the board
// keeps the entries itself. uninstall writes codex-hooks.disabled, so the
// board keeps them out; install clears it.
func runCodexHooks(out io.Writer, args []string, configDir string) error {
	verb := "status"
	if len(args) > 0 {
		verb = args[0]
	}
	if verb == "-h" || verb == "--help" {
		_, err := fmt.Fprintf(out, "usage: %s %s\n", Name, codexHooksUsage)
		return err
	}
	path, err := hooks.CodexConfigPath()
	if err != nil {
		return err
	}
	manager := hooks.NewManager(configDir)
	bin := launch.Installed()
	if bin == "" {
		bin = launch.Executable()
	}
	switch {
	case verb == "install" && len(args) == 1:
		if err := manager.SetCodexDisabled(false); err != nil {
			return err
		}
		changed, err := hooks.RegisterCodex(path, configDir, bin)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, describeChange(changed,
			"registered Gate Inbox's codex hooks in "+path+"; codex asks once to trust them (\"Hooks need review\")",
			path+" already carries Gate Inbox's codex hooks"))
		return err
	case verb == "uninstall" && (len(args) == 1 || len(args) == 2 && args[1] == "--all"):
		scope := configDir
		if len(args) == 2 {
			scope = ""
		}
		changed, err := hooks.UnregisterCodex(path, scope)
		if err != nil {
			return err
		}
		if err := manager.SetCodexDisabled(true); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, describeChange(changed, "removed Gate Inbox's codex hooks from "+path, path+" carries none of Gate Inbox's codex hooks"))
		return err
	case verb == "status" && len(args) <= 1:
		registered, err := hooks.CodexRegistered(path, configDir, bin)
		if err != nil {
			return err
		}
		state := "codex hooks not registered"
		if registered {
			state = "codex hooks registered"
		}
		switch {
		case !configCodexOn(configDir):
			state += " (switched off by [codex] setup = false in config.toml; the board removes them)"
		case manager.CodexDisabled():
			state += " (switched off by codex-hooks uninstall; the board removes them)"
		}
		_, err = fmt.Fprintf(out, "%s: %s\n", path, state)
		return err
	}
	return fmt.Errorf("usage: %s %s", Name, codexHooksUsage)
}

// configCodexOn reads [codex] setup from the board's config, without
// writing a default config where there is none.
func configCodexOn(configDir string) bool {
	if _, err := os.Stat(filepath.Join(configDir, "config.toml")); err != nil {
		return true
	}
	cfg, err := config.LoadDir(configDir)
	return err != nil || cfg.Codex.SetupOn()
}
