// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension/gitroot"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tracing"
)

type Terminal struct {
	ID         string `json:"id" jsonschema:"managed terminal session id"`
	Name       string `json:"name" jsonschema:"terminal name shown in Gate Inbox"`
	Group      string `json:"group" jsonschema:"group path holding the terminal; empty is the root"`
	Directory  string `json:"directory" jsonschema:"terminal's current working directory, or its launch directory when stopped"`
	Status     string `json:"status" jsonschema:"stored Gate Inbox status"`
	Running    bool   `json:"running" jsonschema:"whether the terminal currently has a live tmux pane"`
	ParentID   string `json:"parent_id" jsonschema:"id of the parent session when nested; empty when un-nested"`
	ParentName string `json:"parent_name" jsonschema:"name of the parent session when nested; empty when un-nested"`
}

type TerminalScreen struct {
	Terminal Terminal `json:"terminal"`
	Output   string   `json:"output" jsonschema:"plain text currently visible in the terminal pane"`
}

// TerminalInput is what one send put into a terminal. Which of the two
// kinds went in is decided here, where the dispatch happens, so neither
// front reads it back off its own arguments.
type TerminalInput struct {
	TerminalID string `json:"terminal_id"`
	Sent       string `json:"sent" jsonschema:"input kind sent: command or keys"`
}

type CreateTerminalOptions struct {
	// Nil inherits the calling session's group; a pointer to an empty string
	// deliberately targets the root group.
	Group     *string
	Directory string
	Nest      *bool
}

type Terminals struct {
	commands
}

func NewTerminals(configDir string, words Vocabulary) *Terminals {
	return newTerminals(configDir, words, tmux.NewWithSocket)
}

func newTerminals(configDir string, words Vocabulary, newDriver func(socket string) (*tmux.Driver, error)) *Terminals {
	return &Terminals{commands: commands{configDir: configDir, words: words, newDriver: newDriver}}
}

// commands is the shared plumbing of every managed-pane command: the
// manager's config directory, the words the calling front speaks, and the
// tmux driver behind its socket.
type commands struct {
	configDir string
	words     Vocabulary
	// newDriver takes the socket rather than resolving one, so a command
	// running inside a pane reaches the same tmux server as the manager
	// that started it, whichever the config names.
	newDriver func(socket string) (*tmux.Driver, error)
}

type runtime struct {
	cfg    config.Config
	words  Vocabulary
	store  *store.Store
	driver *tmux.Driver
}

// open is the fixed cost under every command in this package: the config off
// disk, a tmux connection, and the database. It is traced on its own because
// it is the same work every time, so a command that was slow because opening
// the store was slow reads differently from one that was slow doing what it
// was asked.
func (c *commands) open() (opened *runtime, err error) {
	defer start("sessioncmd.open").done(&err)
	cfg, err := config.LoadDir(c.configDir)
	if err != nil {
		return nil, err
	}
	driver, err := c.newDriver(cfg.TmuxSocket)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(c.configDir, "state.db"))
	if err != nil {
		return nil, err
	}
	return &runtime{cfg: cfg, words: c.words, store: st, driver: driver}, nil
}

func (r *runtime) caller(sessionID string) (store.Session, error) {
	if err := validSession(sessionID); err != nil {
		return store.Session{}, err
	}
	sess, err := r.store.Get(sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, fmt.Errorf("calling session %s no longer exists", sessionID)
	}
	return sess, err
}

func (r *runtime) terminal(id string) (store.Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.Session{}, fmt.Errorf("terminal_id is empty; call %s to get one", r.words.ListTerminals)
	}
	sess, err := r.store.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, fmt.Errorf("terminal %s does not exist; call %s for current ids", id, r.words.ListTerminals)
	}
	if err != nil {
		return store.Session{}, err
	}
	if !r.cfg.Tools[sess.Tool].Shell {
		return store.Session{}, fmt.Errorf("session %s is an agent, not a terminal", id)
	}
	if sess.Archived {
		return store.Session{}, fmt.Errorf("terminal %s is archived; restore it in Gate Inbox first", id)
	}
	return sess, nil
}

func (r *runtime) nestedTerminal(sessionID, terminalID string) (store.Session, error) {
	caller, err := r.caller(sessionID)
	if err != nil {
		return store.Session{}, err
	}
	terminal, err := r.terminal(terminalID)
	if err != nil {
		return store.Session{}, err
	}
	return terminal, nestedUnder(terminal, caller.ID)
}

// nestedUnder is the reach every terminal tool holds a session to: only a
// terminal nested directly under it.
func nestedUnder(terminal store.Session, callerID string) error {
	if terminal.ParentID != callerID {
		return fmt.Errorf("terminal %s is not nested under this session", terminal.ID)
	}
	return nil
}

func (r *runtime) info(sess store.Session, running bool) (Terminal, error) {
	dir := sess.Cwd
	if running {
		if current, err := r.driver.PaneCurrentPath(sess.ID); err == nil {
			dir = current
		}
	}
	parentName := ""
	if sess.ParentID != "" {
		// A parent row that is gone leaves the terminal orphaned, which the
		// list paints un-nested; anything else is a store failure.
		parent, err := r.store.Get(sess.ParentID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return Terminal{}, fmt.Errorf("parent %s of terminal %s: %w", sess.ParentID, sess.ID, err)
		default:
			parentName = parent.Name
		}
	}
	return Terminal{
		ID:         sess.ID,
		Name:       sess.Name,
		Group:      sess.Group,
		Directory:  dir,
		Status:     sess.Status,
		Running:    running,
		ParentID:   sess.ParentID,
		ParentName: parentName,
	}, nil
}

func (t *Terminals) List(sessionID string) (listed []Terminal, err error) {
	defer start("sessioncmd.terminals.list").done(&err)
	runtime, err := t.open()
	if err != nil {
		return nil, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return nil, err
	}
	sessions, err := runtime.store.ListSessions(false)
	if err != nil {
		return nil, err
	}
	panes, err := runtime.driver.Panes()
	if err != nil {
		return nil, err
	}
	terminals := make([]Terminal, 0)
	for _, sess := range sessions {
		if !runtime.cfg.Tools[sess.Tool].Shell {
			continue
		}
		_, running := panes[sess.ID]
		info, err := runtime.info(sess, running)
		if err != nil {
			return nil, err
		}
		terminals = append(terminals, info)
	}
	return terminals, nil
}

func (t *Terminals) Create(sessionID string, opts CreateTerminalOptions) (created Terminal, err error) {
	// A terminal is a tmux pane running a shell, so this forks a process and
	// the caller sits there until it exists.
	defer start("sessioncmd.terminals.create", sessionAttr(sessionID)).done(&err)
	runtime, err := t.open()
	if err != nil {
		return Terminal{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return Terminal{}, err
	}
	toolName, tool, ok := runtime.cfg.ShellTool()
	if !ok {
		return Terminal{}, errors.New("no shell configured; add a tool block with shell = true to config.toml")
	}
	nest := true
	if opts.Nest != nil {
		nest = *opts.Nest
	}
	if nest && opts.Group != nil && strings.TrimSpace(*opts.Group) != caller.Group {
		return Terminal{}, fmt.Errorf("set nest false to place in another group")
	}
	group, dir, err := runtime.createTarget(caller, opts.Group, opts.Directory)
	if err != nil {
		return Terminal{}, err
	}
	// A shell caller is a terminal itself, and nesting is one level, so the
	// new shell joins it as a sibling instead of hanging under it.
	callerIsShell := runtime.cfg.Tools[caller.Tool].Shell
	parentID := ""
	if nest {
		parentID = caller.ID
		if callerIsShell {
			parentID = caller.ParentID
		}
	}
	name, err := runtime.shellName(toolName, parentID)
	if err != nil {
		return Terminal{}, err
	}
	sess := store.Session{
		ID:     uuid.NewString()[:8],
		Name:   name,
		Tool:   toolName,
		Cwd:    dir,
		Group:  group,
		Status: status.Starting,
	}
	create := runtime.store.LaunchSession
	if nest {
		if callerIsShell {
			create = func(row store.Session, launch func() error) error {
				return runtime.store.LaunchSessionBeside(row, caller.ID, launch)
			}
		} else {
			// A terminal is a leaf, so it may hang off a caller that is
			// itself a child -- which every agent spawned through
			// create_session now is.
			create = func(row store.Session, launch func() error) error {
				row.ParentID = caller.ID
				return runtime.store.LaunchSessionLeaf(row, launch)
			}
		}
	}
	launched := false
	if err := create(sess, func() error {
		err := runtime.driver.Create(sess.ID, sess.Cwd, tool.Command, nil, 0, 0)
		launched = err == nil
		return err
	}); err != nil {
		if launched {
			if killErr := runtime.driver.Kill(sess.ID); killErr != nil {
				return Terminal{}, fmt.Errorf("%w; its pane %s is still running and has no row: %w", err, sess.ID, killErr)
			}
		}
		return Terminal{}, err
	}
	if nest {
		stored, err := runtime.store.Get(sess.ID)
		if err != nil {
			return Terminal{}, err
		}
		sess = stored
	}
	_ = runtime.driver.SetLabel(sess.ID, sessionLabel(sess.Group, sess.Name))
	return runtime.info(sess, true)
}

func (t *Terminals) Close(sessionID, terminalID string) (err error) {
	defer start("sessioncmd.terminals.close", tracing.Attr{Key: "terminal", Value: terminalID}).done(&err)
	runtime, err := t.open()
	if err != nil {
		return err
	}
	defer runtime.store.Close()
	sess, err := runtime.nestedTerminal(sessionID, terminalID)
	if err != nil {
		return err
	}
	return runtime.store.DeleteChild(sess.ID, sess.ParentID, func() error {
		return runtime.driver.Kill(sess.ID)
	})
}

func (r *runtime) shellName(toolName, parentID string) (string, error) {
	sessions, err := r.store.ListSessions(true)
	if err != nil {
		return "", err
	}
	return ShellName(toolName, parentID, uuid.NewString()[:4], sessions), nil
}

// ShellName names a terminal after the session it hangs under, so a row
// says which session opened it rather than four random digits. A terminal
// with no session over it falls back to those digits, and one joining
// terminals already named for that session counts up. Both the list's T
// and the terminal tools name through here, so a shell reads the same
// whichever opened it.
func ShellName(toolName, parentID, fallbackSuffix string, sessions []store.Session) string {
	parentName := ""
	taken := make(map[string]bool, len(sessions))
	for _, sess := range sessions {
		taken[sess.Name] = true
		if sess.ID == parentID {
			parentName = sess.Name
		}
	}
	if parentName == "" {
		return toolName + "-" + fallbackSuffix
	}
	base := toolName + "-" + parentName
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return name
}

// createTarget resolves the group and directory a new pane opens in.
// A nil requested group inherits the caller's; an explicit one must
// already exist. An explicit directory wins outright; a caller that named
// a group falls back to that group's nearest inherited default path; a
// caller that named none opens beside itself.
func (r *runtime) createTarget(caller store.Session, requestedGroup *string, directory string) (string, string, error) {
	group := caller.Group
	groups, err := r.store.Groups()
	if err != nil {
		return "", "", err
	}
	byName := make(map[string]store.Group, len(groups))
	archived := make(map[string]bool, len(groups))
	for _, candidate := range groups {
		byName[candidate.Name] = candidate
		archived[candidate.Name] = candidate.Archived
	}
	if requestedGroup != nil {
		group = strings.TrimSpace(*requestedGroup)
		if group != "" {
			if _, ok := byName[group]; !ok {
				return "", "", fmt.Errorf("group %q does not exist; call %s for the existing ones or %s to add it", group, r.words.ListGroups, r.words.CreateGroup)
			}
		}
	}
	if group != "" && store.EffectivelyArchived(archived, group) {
		return "", "", fmt.Errorf("group %q is archived; restore it in Gate Inbox first", group)
	}
	if strings.TrimSpace(directory) != "" {
		dir, err := resolveTerminalDirectory(directory)
		return group, dir, err
	}
	if requestedGroup != nil {
		for current := group; current != ""; current = parentGroup(current) {
			if candidate := byName[current].Path; candidate != "" {
				if dir, err := resolveTerminalDirectory(candidate); err == nil {
					return group, dir, nil
				}
			}
		}
	}
	dir := caller.Cwd
	if current, err := r.driver.PaneCurrentPath(caller.ID); err == nil {
		dir = current
	}
	resolved, err := resolveTerminalDirectory(dir)
	if err != nil {
		return "", "", fmt.Errorf("no usable directory for terminal: %w", err)
	}
	return group, resolved, nil
}

// launchDirectory is the directory a spawned agent's pane is opened in, which
// is not always the directory it is asked to work in. It returns the requested
// directory unchanged for almost every spawn.
//
// Claude Code raises a first-run trust dialog keyed on the directory it starts
// in, with its default cursor on "No, exit", so a child spawned into a
// directory the CLI has never been trusted with dies there without reaching a
// first turn -- 16 sessions on this machine, all of them spawned children,
// against not one of the operator's own. Trust is inherited downward: a
// brand-new directory under an accepted one starts clean. So a spawn inside the
// caller's own tree needs nothing, and a spawn outside it opens in the caller's
// launch directory instead, with the requested directory handed to the agent as
// a change-directory step (launch.WorkdirDirective).
//
// The caller's launch directory is the trust this can determine reliably: the
// caller is a live agent that got as far as asking for a child, so whatever its
// CLI thinks of where it started, it started there. The alternative -- the
// nearest ancestor the CLI's own trusted-folders state already covers -- fits
// the problem more closely and is not knowable here: that state is private to
// each CLI, only some of the tools this manager launches keep one at all, and
// its inheritance rules are undocumented, so reading it wrong picks a directory
// that dies exactly as the requested one would have. Trust is per-CLI too, so a
// caller on one tool vouching for a child on another is weaker evidence than it
// looks -- still strictly better than a directory with no evidence at all. A
// caller whose own directory has since gone is no evidence either, and falls
// back to the requested one rather than opening no pane.
//
// This also takes createTarget's PaneCurrentPath fallback out of the trust
// question: a manager that has cd-ed somewhere still hands its children that
// directory to work in, but no longer hands them an untrusted one to start in.
func launchDirectory(callerCwd, requested string) string {
	caller := strings.TrimSpace(callerCwd)
	if caller == "" || requested == "" || gitroot.Within(requested, caller) {
		return requested
	}
	if _, err := resolveTerminalDirectory(caller); err != nil {
		return requested
	}
	return caller
}

func resolveTerminalDirectory(raw string) (string, error) {
	dir := strings.TrimSpace(raw)
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if dir == "~" {
			dir = home
		} else {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("directory %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

func parentGroup(group string) string {
	if index := strings.LastIndex(group, "/"); index >= 0 {
		return group[:index]
	}
	return ""
}

func sessionLabel(group, name string) string {
	if group == "" {
		return name
	}
	return group + " · " + name
}

func (t *Terminals) Send(sessionID, terminalID, command string, keys []string) (input TerminalInput, err error) {
	// Which of the two kinds went in, never the command itself: that text is
	// typed by an agent or a person and runs on this machine, and the span
	// leaves it.
	op := start("sessioncmd.terminals.send", tracing.Attr{Key: "terminal", Value: terminalID})
	defer func() { op.done(&err, tracing.Attr{Key: "sent", Value: input.Sent}) }()
	hasCommand := strings.TrimSpace(command) != ""
	hasKeys := len(keys) > 0
	if hasCommand == hasKeys {
		return TerminalInput{}, errors.New("provide exactly one of command or keys")
	}
	for _, key := range keys {
		if key == "" {
			return TerminalInput{}, errors.New("keys cannot contain an empty value")
		}
	}
	runtime, err := t.open()
	if err != nil {
		return TerminalInput{}, err
	}
	defer runtime.store.Close()
	terminal, err := runtime.nestedTerminal(sessionID, terminalID)
	if err != nil {
		return TerminalInput{}, err
	}
	if !runtime.driver.Exists(terminal.ID) {
		return TerminalInput{}, fmt.Errorf("terminal %s is not running; revive it in Gate Inbox first", terminal.ID)
	}
	if hasCommand {
		if err := runtime.driver.SendText(terminal.ID, command); err != nil {
			return TerminalInput{}, err
		}
		return TerminalInput{TerminalID: terminal.ID, Sent: "command"}, nil
	}
	if err := runtime.driver.SendKeys(terminal.ID, keys...); err != nil {
		return TerminalInput{}, err
	}
	return TerminalInput{TerminalID: terminal.ID, Sent: "keys"}, nil
}

func (t *Terminals) Read(sessionID, terminalID string) (screen TerminalScreen, err error) {
	defer start("sessioncmd.terminals.read", tracing.Attr{Key: "terminal", Value: terminalID}).done(&err)
	runtime, err := t.open()
	if err != nil {
		return TerminalScreen{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return TerminalScreen{}, err
	}
	terminal, err := runtime.terminal(terminalID)
	if err != nil {
		return TerminalScreen{}, err
	}
	if !runtime.driver.Exists(terminal.ID) {
		return TerminalScreen{}, fmt.Errorf("terminal %s is not running; revive it in Gate Inbox first", terminal.ID)
	}
	output, err := runtime.driver.CapturePane(terminal.ID)
	if err != nil {
		return TerminalScreen{}, err
	}
	info, err := runtime.info(terminal, true)
	if err != nil {
		return TerminalScreen{}, err
	}
	return TerminalScreen{
		Terminal: info,
		Output:   strings.TrimRight(ansi.Strip(output), "\r\n"),
	}, nil
}
