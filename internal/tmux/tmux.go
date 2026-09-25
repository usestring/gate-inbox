// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package tmux

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/usestring/gate-inbox/internal/deps"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/tmuxguard"
)

const prefix = "gi_"

// DefaultSocket is tmux's own default server, the one a bare `tmux attach`
// reaches. The manager runs its sessions there so they can be attached and
// driven by hand like any other session.
const DefaultSocket = "default"

// SocketEnv names a different tmux server to run the sessions on, for putting
// the manager back on a private socket of its own.
const SocketEnv = "GATE_INBOX_TMUX_SOCKET"

// requestOption is the global tmux user option the in-session bindings set
// on their way out, naming what the manager should do with the session they
// just detached from.
const requestOption = "@gi_request"

const RequestEditor = "editor"

type Driver struct {
	bin    string
	socket string
	// owned is false on tmux's default server, which belongs to whoever is
	// at the keyboard: their sessions sit beside the managed ones, so every
	// server-scoped command the manager issues would reconfigure or tear
	// down work it never started. Server-scoped work is suppressed or
	// re-scoped to a single session when this is false.
	owned bool

	// adopted holds a Target for every session the manager took over rather
	// than created. The poll loop and the UI drive the same driver, so every
	// command resolves its target through this map under a lock.
	adoptedMu sync.RWMutex
	adopted   map[string]Target

	// controls is the per-socket budget that caps how many control-mode
	// clients this driver may hold on any one tmux server.
	controls controlPool

	// echoWaitOverride replaces echoWait when non-zero. It exists for the
	// test that asserts the paste window is waited out: read it through
	// pasteWindow, never directly, so zero keeps production behaviour.
	echoWaitOverride time.Duration

	// sending holds, per session, a channel closed once the paste in that
	// pane has been submitted. A submit deferred off the caller's goroutine
	// leaves the pane mid-send, and a second paste arriving before the
	// Enter would be swept up by it and submitted as one message, so every
	// paste claims the pane through this and waits its turn. See claimPane.
	sendingMu sync.Mutex
	sending   map[string]chan struct{}

	// captures is the poll loop's control-mode client per tmux server,
	// held open across passes so a board of panes costs no process forks.
	captures captureClients

	// anchors is the tmux servers this manager has put an anchor session on,
	// so exit can take them down again rather than leaving one behind per
	// server per run.
	anchorsMu sync.Mutex
	anchors   map[string]bool

	// pins is every window Resize took over, keyed by server and window id,
	// carrying the window-size each had before it did; pinBySession points a
	// session at the window it is previewed through. See pin.go.
	pinsMu       sync.Mutex
	pins         map[string]*windowPin
	pinBySession map[string]string
	// journalPath is where the live pin set is written so a manager that is
	// killed outright leaves the next run something to restore from. See
	// pinjournal.go.
	journalPath string

	attachSizeLargest atomic.Bool
	paneTheme         atomic.Pointer[PaneTheme]
	paneThemePush     sync.Mutex
}

// PaneTheme is the background agent panes are rendered on. The manager
// knows that color — it paints every capture on it and repaints the
// terminal to it for a full-screen attach — but an agent inside a pane
// cannot discover it: a session nobody has attached to has only a
// control-mode client, so there is no terminal to answer an OSC 11
// background query, and the environment carries no COLORFGBG either.
// Declaring both hands an auto-detecting agent the answer the manager
// already renders, instead of leaving it to guess.
type PaneTheme struct {
	Background string // "#rrggbb"; tmux answers pane OSC 11 queries with it
	ColorFgBg  string // "fg;bg" color indexes for agents reading COLORFGBG
}

// paneThemeArgs is the option pair as a tmux command list, written at the
// scope its flags name. Both options are server-global under "-g", which is
// right only on the manager's own socket, where every session wants the same
// answer and a global option also reaches windows opened inside a session
// later. On a shared server the scope is one session instead: "-g" there
// would repaint and re-env every pane the operator has open. A session scope
// reaches that session's current window only, so a window opened inside a
// managed session afterwards keeps the server's own colours.
//
// An empty Background is the manager drawing on the terminal's own backdrop:
// the panes must carry no background of ours either. That unsets the option
// rather than merely skipping it, because window-style outlives the process
// that wrote it -- a session left painted by an earlier run would otherwise
// keep that backdrop for as long as it lives. COLORFGBG still goes out: an
// agent in a session nobody has attached to has nothing else to read.
func paneThemeArgs(t PaneTheme, scope ...string) []string {
	args := append([]string{"set-option"}, scope...)
	if t.Background == "" {
		args = append(args, "-u", "window-style")
	} else {
		args = append(args, "window-style", "bg="+t.Background)
	}
	args = append(args, ";", "set-environment")
	args = append(args, scope...)
	return append(args, "COLORFGBG", t.ColorFgBg)
}

func (d *Driver) themeScope(session string) []string {
	if d.owned {
		return []string{"-g"}
	}
	return []string{"-t", session}
}

// PublishPaneTheme records the pane colors so Create hands them to new
// sessions. It only stores the value; PushPaneTheme sends it to a running
// server. Recording is synchronous so a session created right after a theme
// change still opens on the chosen background.
func (d *Driver) PublishPaneTheme(t PaneTheme) {
	d.paneTheme.Store(&t)
}

// PushPaneTheme sends the recorded pane theme to a running server. It always
// pushes the latest published value under a lock, so concurrent pushes are
// latest-wins: whichever runs last writes the current theme rather than an
// older one it was spawned for. A server with no sessions exits immediately,
// so there is nothing to push to before the first session exists; Create
// re-applies the recorded theme in the same command list as its new-session,
// which is also what keeps the option set before the agent process can query
// it.
func (d *Driver) PushPaneTheme() error {
	d.paneThemePush.Lock()
	defer d.paneThemePush.Unlock()
	theme := d.paneTheme.Load()
	if theme == nil {
		return nil
	}
	if d.owned {
		return d.pushPaneTheme(*theme, d.themeScope(""))
	}
	// A shared server has no scope that means "the manager's sessions", so
	// the theme is written to each of them by name.
	names, err := d.managedSessions()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := d.pushPaneTheme(*theme, d.themeScope(name)); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) pushPaneTheme(theme PaneTheme, scope []string) error {
	out, err := d.combined(d.args(paneThemeArgs(theme, scope...)...))
	if err != nil {
		if noServer(string(out)) {
			return nil
		}
		return fmt.Errorf("tmux set pane theme: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// managedSessions is the live sessions the manager created, by tmux name. A
// server that is not running holds none rather than failing: the manager
// outlives the sessions it opens.
func (d *Driver) managedSessions() ([]string, error) {
	out, err := d.combined(d.args("list-sessions", "-F", "#{session_name}"))
	if err != nil {
		if noServer(string(out)) {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name := strings.TrimSpace(line); managedName(name) {
			names = append(names, name)
		}
	}
	return names, nil
}

// NewWithSocket builds a driver bound to a named tmux server. An empty name
// takes the socket from the environment and then falls back to tmux's own
// default server. Tests pass an explicit private socket so their sessions
// never collide with the sessions already running on that server.
func NewWithSocket(socket string) (*Driver, error) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("tmux not found on PATH: %w\n%s", err, deps.Hint("tmux"))
	}
	socket = resolveSocket(socket)
	if err := tmuxguard.Err([]string{"-L", socket}); err != nil {
		return nil, err
	}
	return &Driver{bin: bin, socket: socket, owned: OwnsSocket(socket)}, nil
}

// resolveSocket is the tmux server a configured value names: the value
// itself, then the environment override, then tmux's own default server.
func resolveSocket(configured string) string {
	if socket := strings.TrimSpace(configured); socket != "" {
		return socket
	}
	if socket := strings.TrimSpace(os.Getenv(SocketEnv)); socket != "" {
		return socket
	}
	return DefaultSocket
}

// OwnsSocket reports whether a socket is a server the manager may configure
// and tear down as a whole. Only a server it put itself on: tmux's default
// one is the operator's, and everything on it that the manager did not start
// has to survive the manager exactly as it was.
func OwnsSocket(socket string) bool {
	return resolveSocket(socket) != DefaultSocket
}

func (d *Driver) SocketName() string {
	return d.socket
}

// managedName reports whether a tmux session name is one of the manager's
// agent sessions, which on a shared server is the only thing separating its
// own sessions from everything else running there.
//
// The control-client anchor carries the prefix and is not one of them: it
// holds no agent, belongs on no board, and wants none of the chrome, theme or
// liveness a session gets.
func managedName(name string) bool {
	return strings.HasPrefix(name, prefix) && name != anchorSession
}

// Managed is managedName for callers outside this package. Adoption needs it:
// the manager shares tmux's default server with the operator, and whether a
// scanned pane sits in a session the manager created is what decides whether
// that session or that pane is the unit of an agent.
func Managed(name string) bool {
	return managedName(name)
}

// SessionID excludes the poll anchor, which has no session row to recover.
func SessionID(name string) (string, bool) {
	if !managedName(name) {
		return "", false
	}
	id := strings.TrimPrefix(name, prefix)
	return id, id != ""
}

func sessionName(id string) string {
	return prefix + id
}

func SessionName(id string) string {
	return sessionName(id)
}

// Target is where a session's pane actually lives: which tmux server to talk
// to, and what to write after -t once there. A managed session is the session
// named gi_<id> on the manager's own socket. An adopted session is a pane the
// manager never created, named by its pane id ("%12") on whichever server
// owns it; that id is stable for the life of the pane and is a valid -t
// target for send-keys, capture-pane, display and resize alike.
type Target struct {
	// Socket is the -L server name. Adopt fills an empty one in with the
	// manager's own socket.
	Socket string
	// Name is the -t target: "gi_<id>" for a managed session, a pane id for
	// an adopted one.
	Name string
}

// ErrAdopted marks every refusal that exists because the manager did not
// create the pane, so a caller can tell "not allowed here" from a tmux
// failure.
var ErrAdopted = errors.New("session is adopted, not managed")

// Adopt records where a session the manager did not create lives, so every
// id-taking method reaches that pane on that server instead of gi_<id> here.
// Managed sessions are never registered: they keep resolving by name, which
// is what keeps their behaviour identical to before any adoption existed.
func (d *Driver) Adopt(id string, target Target) error {
	if id == "" {
		return errors.New("adopt: empty session id")
	}
	if target.Name == "" {
		return fmt.Errorf("adopt %s: empty tmux target", id)
	}
	if target.Socket == "" {
		target.Socket = d.socket
	}
	d.adoptedMu.Lock()
	defer d.adoptedMu.Unlock()
	if d.adopted == nil {
		d.adopted = map[string]Target{}
	}
	d.adopted[id] = target
	return nil
}

// Release stops driving an adopted session. The pane keeps running: the
// manager never owned it, so letting go is the only way to end the
// relationship.
//
// Letting go includes the window sizing. A pane the manager has stopped
// previewing has no reason to stay pinned to the size of a preview panel,
// and the operator whose window it is has no way to tell that it is.
func (d *Driver) Release(id string) {
	d.unpinSession(id)
	d.adoptedMu.Lock()
	defer d.adoptedMu.Unlock()
	delete(d.adopted, id)
}

// AdoptedTarget reports the recorded target for an adopted session. A managed
// session has no entry, which is the test every refusal below makes.
func (d *Driver) AdoptedTarget(id string) (Target, bool) {
	d.adoptedMu.RLock()
	defer d.adoptedMu.RUnlock()
	target, ok := d.adopted[id]
	return target, ok
}

// TargetFor resolves a session id to the server and target that reach it.
func (d *Driver) TargetFor(id string) Target {
	if target, ok := d.AdoptedTarget(id); ok {
		return target
	}
	return Target{Socket: d.socket, Name: sessionName(id)}
}

// TargetName is what a caller assembling its own tmux command line writes
// after -t. Prefer it to SessionName, which cannot know about an adopted
// pane and would aim the command at a session that does not exist.
func (d *Driver) TargetName(id string) string {
	return d.TargetFor(id).Name
}

// refuseAdopted guards everything the manager may only do to a session it
// created itself.
func (d *Driver) refuseAdopted(id, operation string) error {
	target, ok := d.AdoptedTarget(id)
	if !ok {
		return nil
	}
	return fmt.Errorf("%s: refusing to %s %s on tmux server %q: %w", id, operation, target.Name, target.Socket, ErrAdopted)
}

// tmux requires -L <socket> before the command word, and it is always named
// rather than left to tmux to infer: a bare command takes its server from
// $TMUX, so the manager -- which runs inside a pane itself -- would aim at
// whatever server it is sitting in. args names the manager's own server and
// never an adopted one, because what rides on it is server-wide: the same
// bind-key -n that gives a managed session C-q, C-\, C-r and M-o would seize
// those four keys across every session on the server. That server is now the
// operator's own by default, so the server-wide work is filtered before it
// gets here -- see owned. Work scoped to one session goes through argsAt.
func (d *Driver) args(a ...string) []string {
	return append([]string{"-L", d.socket}, a...)
}

// argsAt aims one session's command at the server that actually holds it.
func (d *Driver) argsAt(id string, a ...string) []string {
	return append([]string{"-L", d.TargetFor(id).Socket}, a...)
}

func (d *Driver) run(args ...string) (string, error) {
	out, err := d.combined(d.args(args...))
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// runAt is run against a session's own server.
func (d *Driver) runAt(id string, args ...string) (string, error) {
	out, err := d.combined(d.argsAt(id, args...))
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// commandListBudget is how many bytes of arguments one tmux invocation may
// carry. tmux answers a longer one with "command too long" and runs none of
// it, and the ceiling is the whole list's bytes rather than a count of the
// commands in it: measured on tmux 3.4, 150 chained set-options totalling
// 15,040 bytes are accepted where 180 totalling 18,070 are refused, and 400
// short ones pass where 250 long ones do not. The budget sits under that so
// a caller that measures before it sends is never the one that finds the
// edge.
const commandListBudget = 12 << 10

// splitCommandList cuts commands into lists small enough for tmux to accept,
// keeping their order.
//
// A caller that bounds a batch by how many ITEMS it covers is guessing, because
// the bytes each item contributes are not its to know: a session's chrome
// carries its label, and a label is whatever someone named a run. Thirty-two
// sessions of eleven commands were within the ceiling for two years of short
// names and over it the day a fleet ran a long label, which is how a working
// batch size becomes a failing one with no code change at all.
//
// A single command over the budget is returned in a list of its own: it will
// fail, and failing as itself is what lets the caller's per-item retry say
// which one.
func splitCommandList(commands [][]string) [][][]string {
	var lists [][][]string
	var current [][]string
	size := 0
	for _, command := range commands {
		cost := len(command) + 1 // the ";" that joins it to the one before
		for _, arg := range command {
			cost += len(arg)
		}
		if len(current) > 0 && size+cost > commandListBudget {
			lists = append(lists, current)
			current, size = nil, 0
		}
		current = append(current, command)
		size += cost
	}
	if len(current) > 0 {
		lists = append(lists, current)
	}
	return lists
}

// commandList joins commands into the single invocation tmux takes for a
// whole list, where any argument ending in ";" ends one command and starts
// the next, losing that character: a value that has to keep a trailing
// semicolon writes it as \;. tmux stops at the first command that fails and
// exits non-zero, so the list reports a failure the way a run per command
// did.
func commandList(commands ...[]string) []string {
	var args []string
	for _, command := range commands {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, command...)
	}
	return args
}

// afterCreateThemeLoad runs between Create loading the pane theme and writing
// it, while Create holds the push lock. Nil in production; a test sets it to
// drive a push against the held lock and prove the write ordering.
var afterCreateThemeLoad func()

func (d *Driver) Create(id, cwd, command string, env map[string]string, width, height int) error {
	// An adopted id already names a running pane. Creating gi_<id> beside it
	// would leave the manager driving one pane and reviving into another.
	if err := d.refuseAdopted(id, "create a session for"); err != nil {
		return err
	}
	d.forgetPin(id)
	name := sessionName(id)
	var args []string
	// Ahead of the push lock: the first session opened under a shell starts it
	// once to see what it can do, and a theme write must not queue behind that.
	var shell launchShell
	if command != "" {
		shell = resolveLaunchShell()
	}
	// Hold the push lock across loading the theme and the command list that
	// writes it, so a concurrent PushPaneTheme cannot land a newer theme
	// between the load and the write and be clobbered by this stale one.
	d.paneThemePush.Lock()
	// Ahead of new-session in the same command list, so the options are in
	// place before the pane process exists and can query its background. A
	// session-scoped theme cannot go first -- its target does not exist until
	// new-session has run -- so it trails in the same list instead, which
	// still lands before the shell in the pane has drawn anything.
	var colorFgBg string
	var trailingTheme []string
	if theme := d.paneTheme.Load(); theme != nil {
		colorFgBg = theme.ColorFgBg
		scope := d.themeScope(name)
		if d.owned {
			args = append(paneThemeArgs(*theme, scope...), ";")
		} else {
			trailingTheme = append([]string{";"}, paneThemeArgs(*theme, scope...)...)
		}
	}
	if afterCreateThemeLoad != nil {
		afterCreateThemeLoad()
	}
	args = append(args, "new-session", "-d", "-s", name, "-c", cwd)
	// A detached session sizes to tmux's 80x24 default and holds it until a
	// client attaches, so its pane preview renders narrow. Booting at the
	// preview panel's size makes the preview fit from the first frame.
	if width > 0 && height > 0 {
		args = append(args, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height))
	}
	// Launch by handing a script to a shell in a short window command.
	// Typing the full line with send-keys truncates around 1024 bytes, which
	// breaks long first prompts mid-path. A script has no practical length
	// limit, and exec'ing the user shell afterwards matches "type into a
	// shell" (pane stays up).
	var scriptPath string
	if command != "" {
		var err error
		scriptPath, err = writeLaunchScript(d.launchScriptPath(id), env, command, colorFgBg, shell)
		if err != nil {
			d.paneThemePush.Unlock()
			return err
		}
		args = append(args, shell.runScript(scriptPath))
	}
	args = append(args, trailingTheme...)
	_, runErr := d.run(args...)
	d.paneThemePush.Unlock()
	if runErr != nil {
		if scriptPath != "" {
			os.Remove(scriptPath)
		}
		return runErr
	}
	if err := d.installSessionUX(id); err != nil {
		_ = d.Kill(id)
		return err
	}
	return nil
}

// ShellQuote wraps a string in single quotes for POSIX sh; the config
// dir on macOS contains a space, so paths sent into panes must be quoted.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// launchScriptPath names the socket as well as the session: two servers can
// hold sessions with the same id (a test run beside the live inbox, or the
// concurrent shards of one test run), and a shared path let one server's
// Kill delete the script another's pane was about to run.
func (d *Driver) launchScriptPath(id string) string {
	h := fnv.New32a()
	h.Write([]byte(d.socket))
	return filepath.Join(os.TempDir(), fmt.Sprintf("gi-launch-%08x-%s.sh", h.Sum32(), id))
}

// writeLaunchScript writes the script a pane runs. The body stays POSIX: the
// shell it runs under is the operator's, and runsLoginScript is what vouches
// for that shell being able to read it.
//
// The session environment rides as export lines in the script body rather
// than assignments prefixed to the command: a prefix would put every value,
// including API keys, in the process listing for anyone on the box to read,
// while the script file itself is owner-only. The exports also reach the
// shell the pane drops to once the agent exits, not just the agent process.
func writeLaunchScript(path string, env map[string]string, command, colorFgBg string, shell launchShell) (string, error) {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var body strings.Builder
	body.WriteString("#!/bin/sh\n")
	for _, key := range keys {
		body.WriteString("export " + key + "=" + ShellQuote(env[key]) + "\n")
	}
	// Export COLORFGBG in the pane itself. The global option tmux carries in
	// its environment does not reach this first process — it inherits the
	// server's own environment, fixed when the server started, so a host
	// shell that exports COLORFGBG hands the agent that stale pair instead.
	// Exporting here lands the theme's value on the agent regardless, and it
	// comes last so it wins over anything the session environment carried.
	if colorFgBg != "" {
		body.WriteString("export COLORFGBG=" + ShellQuote(colorFgBg) + "\n")
	}
	if shell.readsStartupFiles() {
		// Startup files print: a themed prompt, a MOTD, an oh-my-zsh notice.
		// The pane's first bytes are also what decides a session has stopped
		// starting up, so the banner is wiped before the agent runs rather
		// than read as its output.
		body.WriteString("printf '\\033[2J\\033[3J\\033[H'\n")
	}
	// A stopped agent is not a finished one. Under the login shell the agent
	// is a job, and a job the terminal is taken from (SIGTTIN when another
	// process makes itself the pane's foreground group, or a stray SIGTSTP)
	// returns control here stopped rather than exited. Falling straight
	// through to the exec below orphans it: the pane drops to a prompt that
	// reads as the agent having died, every message the manager then pastes
	// lands in that prompt, and the agent sits stopped with no shell left to
	// fg it. fg hands the terminal back to the job and continues it, and
	// fails once no job is left, which is the exit the line waits for. Under
	// a shell with no job control fg fails at once and nothing changes.
	body.WriteString(command + "\nwhile fg >/dev/null 2>&1; do :; done\n" + shell.execLine() + "\n")
	if err := os.WriteFile(path, []byte(body.String()), 0o700); err != nil {
		return "", fmt.Errorf("launch script: %w", err)
	}
	return path, nil
}

func (d *Driver) installSessionUX(id string) error {
	if err := d.refuseAdopted(id, "install session UX on"); err != nil {
		return err
	}
	if err := d.EnsureBindings(); err != nil {
		return err
	}
	if err := d.styleStatusBar(id); err != nil {
		return err
	}
	_, err := d.run("set-option", "-t", sessionName(id), "status-left", "")
	return err
}

// styleStatusBar sets a session's status bar chrome, leaving status-left (the
// name label) untouched so re-styling a live session keeps its label. An
// adopted session is refused: these options are session-wide on a server the
// manager does not own, so they would repaint a bar somebody else set up.
func (d *Driver) styleStatusBar(id string) error {
	if err := d.refuseAdopted(id, "restyle the status bar of"); err != nil {
		return err
	}
	name := sessionName(id)
	primary, err := d.run("show-options", "-t", name, "-v", "prefix")
	if err != nil {
		return err
	}
	secondary, err := d.run("show-options", "-t", name, "-v", "prefix2")
	if err != nil {
		return err
	}
	options := [][]string{
		{"set-option", "-t", name, "status", "on"},
		// The default status-right-length of 40 truncates the hints, so widen it
		// to fit the whole footer, including the configured-prefix fallback.
		{"set-option", "-t", name, "status-right-length", "100"},
		{"set-option", "-t", name, "status-right", attachStatusRight(strings.TrimSpace(primary), strings.TrimSpace(secondary))},
		{"set-option", "-t", name, "status-style", "bg=colour236,fg=colour249"},
		// hide the "0:windowname*" window list; it reads as noise here
		{"set-option", "-t", name, "window-status-format", ""},
		{"set-option", "-t", name, "window-status-current-format", ""},
		// mouse on so tmux handles scrollback per-session instead of the
		// terminal emulator, whose buffer carries content from prior attaches.
		{"set-option", "-t", name, "mouse", "on"},
	}
	_, err = d.run(commandList(options...)...)
	return err
}

func attachStatusRight(primary, secondary string) string {
	exits := make([]string, 0, 2)
	if primary != "C-q" && secondary != "C-q" {
		exits = append(exits, "Ctrl+q")
	}
	for _, candidate := range []string{primary, secondary} {
		if candidate != "" && candidate != "None" {
			exits = append(exits, candidate+" d")
			break
		}
	}
	return " Gate Inbox · Alt+o = editor · " + strings.Join(exits, " / ") + " = back "
}

func (d *Driver) EnsureBindings() error {
	inSession := "#{m:" + prefix + "*,#{session_name}}"
	request := func(name string) string {
		return "set-option -g " + requestOption + " " + name + " ; detach-client"
	}
	binds := [][]string{
		{"bind-key", "-n", "C-q", "if-shell", "-F", inSession, "detach-client", "send-keys C-q"},
		{"bind-key", "-n", `C-\`, "if-shell", "-F", inSession, "detach-client", `send-keys C-\\`},
		{"bind-key", "-n", "M-o", "if-shell", "-F", inSession, request(RequestEditor), "send-keys M-o"},
		// The editor used to sit on C-o, which Claude Code and readline
		// both bind, and later on F3; C-r opened the review the manager
		// no longer has. A server that outlives the update still carries those
		// bindings until they are dropped.
		{"unbind-key", "-n", "C-o"},
		{"unbind-key", "-n", "C-r"},
		{"unbind-key", "-n", "F3"},
		// Restore the standard fallback when the prefix shadows a direct binding.
		{"bind-key", "-T", "prefix", "d", "detach-client"},
	}
	if !d.owned {
		var err error
		binds, err = d.sharedBindings(binds)
		if err != nil || len(binds) == 0 {
			return err
		}
	}
	_, err := d.run(commandList(binds...)...)
	return err
}

// sharedBindings is the part of a binding list that may be installed on a
// server the manager does not own.
//
// A root-table bind-key only, and only for a key nothing has bound yet.
// Everything else in the list rewrites configuration that belongs to the
// operator -- an unbind-key drops a binding they may be using, and a binding
// in the prefix table overwrites what their own config put there -- and a key
// already bound is theirs too, however much the manager would like it. A
// binding an earlier run installed reads as bound and is left alone, so a
// changed binding reaches a shared server only after that key comes free.
func (d *Driver) sharedBindings(binds [][]string) ([][]string, error) {
	bound, running, err := d.rootKeys()
	if err != nil {
		return nil, err
	}
	// With no server up there is no session of ours to bind for, and
	// bind-key would start one just to hold the bindings.
	if !running {
		return nil, nil
	}
	var kept [][]string
	for _, bind := range binds {
		if len(bind) < 3 || bind[0] != "bind-key" || bind[1] != "-n" {
			continue
		}
		if bound[bind[2]] {
			continue
		}
		kept = append(kept, bind)
	}
	return kept, nil
}

// rootKeys is every key bound in the root table, and whether a server
// answered at all.
func (d *Driver) rootKeys() (map[string]bool, bool, error) {
	out, err := d.combined(d.args("list-keys", "-T", "root"))
	if err != nil {
		if noServer(string(out)) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("tmux list-keys: %w: %s", err, strings.TrimSpace(string(out)))
	}
	keys := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if key := rootKeyName(line); key != "" {
			keys[key] = true
		}
	}
	return keys, true, nil
}

// rootKeyName reads the key out of one list-keys line, which tmux prints as
// the bind-key command that would recreate the binding.
func rootKeyName(line string) string {
	fields := strings.Fields(line)
	for i := 0; i+2 < len(fields); i++ {
		if fields[i] == "-T" && fields[i+1] == "root" {
			return unquoteKey(fields[i+2])
		}
	}
	return ""
}

// unquoteKey turns a key as list-keys prints it back into the name bind-key
// takes: tmux writes it the way it would parse it again, so a backslash
// arrives escaped and a key whose name needs quoting arrives quoted.
func unquoteKey(key string) string {
	if len(key) >= 2 && strings.HasPrefix(key, "'") && strings.HasSuffix(key, "'") {
		key = key[1 : len(key)-1]
	}
	return strings.ReplaceAll(key, `\\`, `\`)
}

// RefreshChrome re-applies the status bar chrome to a live session so a
// session created before a manager update picks up the current footer,
// without disturbing its name label.
func (d *Driver) RefreshChrome(id string) error {
	return d.styleStatusBar(id)
}

// SendText delivers text into the session's pane and presses Enter, so the
// agent inside receives it as a user message. It returns once the Enter has
// gone out, so it spends whatever window the pane takes to draw the paste;
// a caller that cannot stall for a pane sends with SendTextAsync instead.
func (d *Driver) SendText(id, text string) error {
	submit, err := d.pasteHoldingSubmit(id, text)
	if err != nil {
		return err
	}
	return submit()
}

// SendTextAsync delivers text the way SendText does, but hands the wait for
// the pane to draw the paste, and the Enter that follows it, to a goroutine.
// It returns as soon as the text is in the pane. submitted is called once,
// later, with whatever the submit cost; a paste that never got that far
// reports through the returned error instead and submitted is not called.
//
// A pane mid-turn is exactly the pane that will not draw a paste promptly,
// so this is how the poll pass sends; what that was costing the board is in
// internal/ui/asyncsend.go.
func (d *Driver) SendTextAsync(id, text string, submitted func(error)) error {
	submit, err := d.pasteHoldingSubmit(id, text)
	if err != nil {
		return err
	}
	go func() { submitted(submit()) }()
	return nil
}

// SendKeys delivers exact tmux key names to a session. Keeping each key as
// its own argv entry avoids routing agent-supplied input through a shell.
func (d *Driver) SendKeys(id string, keys ...string) error {
	args := []string{"send-keys", "-t", d.TargetName(id), "--"}
	_, err := d.runAt(id, append(args, keys...)...)
	return err
}

// Paste delivers text into the session's pane without submitting it. The
// focus path uses this for clipboard pastes: sending the bytes as raw
// keystrokes would turn every newline into an Enter press and submit the
// agent's prompt mid-paste.
func (d *Driver) Paste(id, text string) error {
	done := d.claimPane(id)
	defer d.releasePane(id, done)
	return d.paste(id, text)
}

var pasteSeq atomic.Uint64

// Only a pane that never echoes what it reads waits out echoWait; an agent
// redraws a paste in tens of milliseconds, even mid-launch.
const (
	echoWait = time.Second
	echoPoll = 25 * time.Millisecond
)

// pasteWindow is the window a paste is actually given. Production leaves
// echoWaitOverride at zero and so gets echoWait; the override exists so a
// test can watch the window be waited out without waiting it.
func (d *Driver) pasteWindow() time.Duration {
	if d.echoWaitOverride > 0 {
		return d.echoWaitOverride
	}
	return echoWait
}

// pasteHoldingSubmit puts the text in the pane and hands back the Enter it
// is still owed: calling that gives the pane its window to draw the paste
// and then presses Enter. The two are split so the window can be waited
// somewhere other than the caller, never so that it can be skipped -- both
// writes reach one pty, and a pane too busy to read between them takes the
// carriage return as part of the bracketed paste rather than as a submit,
// stranding the message in the composer.
//
// Until the returned submit has run, the pane counts as mid-send: another
// paste landing in it before the Enter would be swept up by that Enter and
// submitted as one message, so the paste paths wait their turn here.
// Operator keystrokes deliberately do not wait -- they already race a
// synchronous send's Enter, and delivery is held off a pane someone is
// typing into by rules further up rather than by a lock down here.
func (d *Driver) pasteHoldingSubmit(id, text string) (func() error, error) {
	done := d.claimPane(id)
	before, baseline := d.capturePlain(id)
	if err := d.paste(id, text); err != nil {
		d.releasePane(id, done)
		return nil, err
	}
	return func() error {
		defer d.releasePane(id, done)
		if baseline != nil {
			// Without a baseline, text already on screen reads as the new
			// paste, so the pane gets the whole window to draw it rather
			// than a match.
			time.Sleep(d.pasteWindow())
		} else {
			d.awaitPasteEcho(id, before, text)
		}
		_, err := d.runAt(id, "send-keys", "-t", d.TargetName(id), "Enter")
		return err
	}, nil
}

// claimPane waits for whatever is part way through this session's pane and
// then claims it, in one step: a two-step look-then-claim would let two
// pastes both find the pane free and interleave in it.
func (d *Driver) claimPane(id string) chan struct{} {
	for {
		d.sendingMu.Lock()
		held := d.sending[id]
		if held == nil {
			done := make(chan struct{})
			if d.sending == nil {
				d.sending = make(map[string]chan struct{})
			}
			d.sending[id] = done
			d.sendingMu.Unlock()
			return done
		}
		d.sendingMu.Unlock()
		<-held
	}
}

func (d *Driver) releasePane(id string, done chan struct{}) {
	d.sendingMu.Lock()
	if d.sending[id] == done {
		delete(d.sending, id)
	}
	d.sendingMu.Unlock()
	close(done)
}

// A pane that draws the paste some other way, as a collapsed placeholder or
// not at all, is released at the cap and submits the way it did before.
func (d *Driver) awaitPasteEcho(id, before, text string) {
	opening := MessageOpening(text)
	if opening == "" {
		return
	}
	was := strings.Count(before, opening)
	deadline := time.Now().Add(d.pasteWindow())
	for {
		if pane, err := d.capturePlain(id); err == nil && strings.Count(pane, opening) > was {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(echoPoll)
	}
}

// MessageOpening is the slice of a message to look for in a pane: its first
// line with anything on it, cut short because a composer wraps a long line
// and would split any longer match. A message that opens on a blank line
// still has to be waited for, so the blank lines are skipped rather than
// answered with nothing to match.
func MessageOpening(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if runes := []rune(line); len(runes) > 16 {
			line = strings.TrimSpace(string(runes[:16]))
		}
		return line
	}
	return ""
}

// paste loads text into a tmux buffer and pastes it into the pane.
// tmux send-keys silently stops around 1024 bytes; load-buffer does not.
func (d *Driver) paste(id, text string) error {
	file, err := os.CreateTemp("", "gi-paste-*")
	if err != nil {
		return fmt.Errorf("paste temp file: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.WriteString(text); err != nil {
		file.Close()
		return fmt.Errorf("paste temp write: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("paste temp close: %w", err)
	}
	buf := fmt.Sprintf("gi_paste_%d", pasteSeq.Add(1))
	if _, err := d.runAt(id, "load-buffer", "-b", buf, path); err != nil {
		return err
	}
	// Preserve bracketed-paste boundaries when the pane application requests
	// them. Codex uses paste-burst detection without these markers and can
	// consume the immediately following Enter as part of the paste, leaving
	// the prompt in its composer instead of submitting it.
	if _, err := d.runAt(id, "paste-buffer", "-p", "-d", "-b", buf, "-t", d.TargetName(id)); err != nil {
		_, _ = d.runAt(id, "delete-buffer", "-b", buf)
		return err
	}
	return nil
}

// SendRaw runs one pre-assembled tmux command line. The focus path builds
// send-keys commands from fixed tokens and hex codes, so whitespace
// splitting is exact; nothing quoted ever rides through here.
func (d *Driver) SendRaw(command string) error {
	_, err := d.run(strings.Fields(command)...)
	return err
}

// SendRawAt runs one pre-assembled tmux command line against the server that
// owns a session. SendRaw carries no id, so it stays on the manager's own
// socket, which reaches nothing for an adopted pane; build the -t target with
// TargetName and send it here instead.
func (d *Driver) SendRawAt(id, command string) error {
	if d.PipeSend(id, command) {
		return nil
	}
	_, err := d.runAt(id, strings.Fields(command)...)
	return err
}

// SetLabel puts the session's name and group path in the status bar's
// left side, replacing the hidden window list.
func (d *Driver) SetLabel(id, label string) error {
	if err := d.refuseAdopted(id, "relabel the status bar of"); err != nil {
		return err
	}
	name := sessionName(id)
	if _, err := d.run("set-option", "-t", name, "status-left-length", "80"); err != nil {
		return err
	}
	if _, err := d.run("set-option", "-t", name, "status-left", " "+sanitizeFormat(label)+" "); err != nil {
		return err
	}
	// The window name is the tab: wherever tmux draws its own window list -- an
	// attached session, another client, the chooser -- a renamed session whose
	// tab still reads the command that started it is one session wearing two
	// names. rename-window also turns tmux's automatic renaming off for that
	// window, so the name survives the next command the pane runs.
	_, err := d.run("rename-window", "-t", name, sanitizeFormat(label))
	return err
}

// sanitizeFormat neutralizes tmux format expansion in user-supplied text.
// Status bars expand #(shell command) and friends, so a session named
// "#(cmd)" would otherwise execute when the bar renders. tmux escapes
// a literal # as ##. Control characters are dropped.
func sanitizeFormat(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.ReplaceAll(s, "#", "##")
}

// A missing tmux server means no request rather than an error: the
// manager outlives the sessions it opens.
func (d *Driver) PendingRequest() (string, error) {
	out, err := d.combined(d.args("show-option", "-gqv", requestOption))
	if err != nil {
		if noServer(string(out)) {
			return "", nil
		}
		return "", fmt.Errorf("tmux show-option %s: %w: %s", requestOption, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// ClearRequest unsets the marker so a request is carried out once.
func (d *Driver) ClearRequest() error {
	_, err := d.run("set-option", "-gu", requestOption)
	return err
}

// AttachCommand hands the terminal over to a session's pane.
//
// $TMUX is dropped from the attach, because the manager now runs its sessions
// on the same server it is itself likely to be running inside, and tmux
// refuses that attach as accidental nesting ("sessions should be nested with
// care"). Here the nesting is the whole point: the manager's screen already
// fills this terminal, and the attach replaces it until the operator detaches.
//
// The one attach that guard was right about is the session the manager is
// running in, which would put its screen inside itself. $TMUX stays on for
// that one so tmux refuses it, and its refusal reaches the operator as the
// error it is.
// The -t is always an exact session name, never the pane. tmux resolves a
// pane id to its session, but attach-session given one also makes that pane's
// window the session's current window -- and every client of a session shows
// the session's current window, so attaching to an adopted pane walked the
// operator's own terminal to another window under them. Measured, not
// reasoned: a session sitting on window 0 with a real client watching it is
// on window 1 the moment the manager attaches to a pane in window 1.
//
// The "=" prefix is what keeps that a property of the code. It forces exact
// session-name matching, so a target that is a pane id cannot quietly resolve
// to the pane's session; tmux refuses it by name instead ("can't find
// session: %1"). A future caller that passes the wrong thing gets an error,
// not somebody else's window.
func (d *Driver) AttachCommand(id string) *exec.Cmd {
	target := d.TargetFor(id)
	session, window, pane := d.attachTarget(target)
	args := []string{"attach-session", "-t", "=" + session}
	if window != "" {
		// Landing on the pane the operator picked, but only when this attach
		// is the session's only client. Selecting a window moves it for every
		// client of the session, which is the harm above; with nobody else
		// there, there is nobody to move.
		//
		// The select runs after the attach, and the guard counts one client
		// rather than none, because that is what removes the race: by then
		// this client exists, so "1" means "and no others", decided inside
		// tmux in the same command list rather than by a count this process
		// read and then acted on. A client that attaches later arrives at the
		// window this one chose, which is what attaching to a session does
		// anyway.
		//
		// The cost when somebody else is watching: the operator lands on
		// whatever window that session is showing, not the pane they picked.
		// Giving them both means a session group (new-session -t), which
		// carries its own current window -- a new session on the operator's
		// server, and a bigger change than this one.
		selects := "select-window -t " + window
		if pane != "" {
			selects += " ; select-pane -t " + pane
		}
		args = append(args, ";", "if-shell", "-F", "-t", "="+session,
			"#{==:#{session_attached},1}", selects)
	}
	tmuxguard.Enforce([]string{"-L", target.Socket})
	cmd := exec.Command(d.bin, append([]string{"-L", target.Socket}, args...)...)
	nested := d.runningInside(target)
	if !nested {
		cmd.Env = envWithoutTmux(os.Environ())
	}
	// The manager handing the terminal to tmux: without a record here, a
	// report that it "dropped out of the TUI" has nothing to check.
	logging.Info("tmux attach", "session", id, "socket", target.Socket,
		"target", "="+session, "window", window, "pane", pane, "nested", nested)
	return cmd
}

// attachTarget is the exact session name to attach to, and for an adopted
// pane the window and pane to land on.
//
// A managed session is its own answer and has nothing to select: the manager
// made it with one window. An adopted pane has to be asked which session it
// is in, because its Target names the pane.
//
// A pane that cannot be resolved -- it closed, the server will not answer --
// returns the pane id as the session. That is deliberate: "=%3" names no
// session, so tmux refuses the attach and says so, where returning the pane
// id unprefixed would attach to its session and move the window. A refusal
// the operator can read beats a side effect they cannot undo.
func (d *Driver) attachTarget(target Target) (session, window, pane string) {
	if !strings.HasPrefix(target.Name, "%") {
		return target.Name, "", ""
	}
	out, err := d.output([]string{"-L", target.Socket, "display-message", "-p",
		"-t", target.Name, "#{session_name}\t#{window_id}"})
	if err != nil {
		return target.Name, "", ""
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(fields) != 2 || fields[0] == "" || fields[1] == "" {
		return target.Name, "", ""
	}
	return fields[0], fields[1], target.Name
}

// runningInside reports whether the manager's own pane is the one this target
// names. Anything it cannot establish -- no tmux around the manager, a pane
// on another server, a server that will not answer -- is a no: the attach
// tmux would refuse is the narrow case, and the common one has to work.
func (d *Driver) runningInside(target Target) bool {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || os.Getenv("TMUX") == "" {
		return false
	}
	out, err := d.output([]string{"-L", target.Socket, "display-message", "-p", "-t", pane, "#{session_name}\t#{pane_id}"})
	if err != nil {
		return false
	}
	for _, field := range strings.Split(strings.TrimSpace(string(out)), "\t") {
		if field == target.Name {
			return true
		}
	}
	return false
}

func envWithoutTmux(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "TMUX=") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// Kill refuses an adopted session. That pane belongs to whoever opened it,
// and killing it would take work the manager never started down with it.
// The refusal stays now that KillAdopted exists, because it is what keeps
// every path that merely wants a session gone — archive, restart, whatever
// is written next — from ending somebody's agent as a side effect. Reaching
// a foreign pane has to be asked for by name.
func (d *Driver) Kill(id string) error {
	if err := d.refuseAdopted(id, "kill"); err != nil {
		return err
	}
	// The window goes with the session, so there is nothing to hand back --
	// but the record has to go too, or a revive under this id would find the
	// dead window already pinned and never record the new one.
	d.forgetPin(id)
	if !d.Exists(id) {
		os.Remove(d.launchScriptPath(id))
		return nil
	}
	_, err := d.run("kill-session", "-t", sessionName(id))
	os.Remove(d.launchScriptPath(id))
	return err
}

// KillAdopted ends a pane the manager never started, the one operation Kill
// will not do. The two are separate methods rather than one method with a
// flag so that the dangerous act is visible where it is called: a reader of
// the call site sees that somebody else's agent is about to die.
//
// The kill is pane-scoped. An adopted target is one pane of a window that may
// hold others belonging to the same person, so kill-session here would take
// down panes the manager never even identified.
//
// The registry entry survives: the pane is gone, but the session is still
// this manager's to show, and its row now reads dead the way a killed managed
// one does. Release is what stops tracking it, and a caller that is dropping
// the session as well calls both.
func (d *Driver) KillAdopted(id string) error {
	target, adopted := d.AdoptedTarget(id)
	if !adopted {
		return fmt.Errorf("%s: not an adopted session, Kill is the way to end it", id)
	}
	if !d.Exists(id) {
		return nil
	}
	_, err := d.runAt(id, "kill-pane", "-t", target.Name)
	return err
}

func (d *Driver) Exists(id string) bool {
	target, adopted := d.AdoptedTarget(id)
	if !adopted {
		return d.silent(d.args("has-session", "-t", sessionName(id))) == nil
	}
	// A pane is not a session, so has-session cannot answer for one. display
	// can, but a pane that has closed is not an error to it: tmux 3.4 prints
	// an empty line and exits 0, which would leave every adopted session
	// looking alive forever. The pane id coming back is the only proof.
	out, err := d.output(d.argsAt(id, "display-message", "-p", "-t", target.Name, "#{pane_id}"))
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// CapturePane returns the visible pane content with ANSI escapes intact
// (-e), so previews keep the session's real colors. Strip before regex use.
func (d *Driver) CapturePane(id string) (string, error) {
	// Forked, never over the pipe: see the note above CaptureScrollback.
	return d.runAt(id, "capture-pane", "-p", "-e", "-t", d.TargetName(id))
}

// CaptureRegion returns a pane's content from start to end, where tmux numbers
// the visible screen from 0 down and history above it with negative lines. A
// scrolled view is just a shifted region.
//
// It forks, on every server, adopted pane or one the manager started. It used
// to take the pooled pipe where a server had a client, and that is what the
// paragraph here used to say; captures came off the pipe when the retention
// the server pays for a control client's replies turned out to be a leak.
// See the note above CaptureScrollback for the measurement.
func (d *Driver) CaptureRegion(id string, start, end int) (string, error) {
	// Forked, never over the pipe: see the note above CaptureScrollback.
	// runAt returns the region verbatim, which is what this caller needs --
	// ExecShape trims before it appends, dropping a region's last line
	// whenever that line is blank, and a frame short by one line is a frame
	// with the wrong rows in it. A full-pane capture can afford the trim
	// because its trailing blanks are tmux's own padding; a region's are
	// lines the operator asked to see.
	return d.runAt(id, "capture-pane", "-p", "-e", "-t", d.TargetName(id),
		"-S", strconv.Itoa(start), "-E", strconv.Itoa(end))
}

// capturePlain drops the escapes CapturePane keeps, which an application is
// free to write partway through a line, breaking a match on the text.
func (d *Driver) capturePlain(id string) (string, error) {
	return d.runAt(id, "capture-pane", "-p", "-t", d.TargetName(id))
}

// Resize pins a session's window to the given dimensions so its preview
// capture fills the manager's preview panel. resize-window forces
// window-size to manual, which is what keeps a detached window fixed;
// PrepareAttach flips it back to auto before a client attaches so the
// window fills the terminal instead of leaving a dotted overlay gap.
//
// Adopted panes are sized the same way, on their own server. The board is
// mostly adopted panes, and one left at whatever size its own window has
// paints a band of output across the top of a much taller panel with nothing
// under it -- the taller the terminal, the more of the panel is dead. What
// keeps the pin from being felt as a terminal resized out from under someone
// is PrepareAttach, which hands the window back to automatic sizing before a
// client attaches.
func (d *Driver) Resize(id string, width, height int) error {
	if width <= 0 || height <= 0 {
		return nil
	}
	target := d.TargetFor(id)
	if d.runningInOwnPane(target) {
		return nil
	}
	// Ahead of the resize, because resize-window writes "manual" over
	// whatever the window had and a value read afterwards is the manager's
	// own. Release and exit put the recorded one back -- see pin.go.
	d.pinWindow(id, target.Socket, target.Name)
	_, err := d.runAt(id, "resize-window", "-t", target.Name, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height))
	return err
}

// runningInOwnPane reports that a target names the very pane the manager is
// drawing in. The scan adopts panes by what is running in them, and the
// manager is often started from a pane an agent already holds, so its own
// window can end up on its own board. Sizing that one to the preview panel
// shrinks the terminal the manager is painting, which arrives back as a
// window-size message and sizes the panel smaller again: a terminal that
// collapses a step per frame.
//
// The answer is read from the environment tmux sets for a process inside a
// pane rather than from the server, because Resize runs once per session on
// every window resize and asking tmux per session would double that.
// OwnPane names the tmux pane the manager itself is drawing in, and the
// server holding it, from the environment tmux sets inside a pane. Both are
// empty when the manager is not running under tmux, which callers read as
// "there is nothing to ask tmux about".
func OwnPane() (pane, socket string) {
	pane = os.Getenv("TMUX_PANE")
	if pane == "" {
		return "", ""
	}
	// $TMUX is "<socket path>,<pid>,<session>"; the server is named by the
	// socket file, which is what -L takes.
	path, _, ok := strings.Cut(os.Getenv("TMUX"), ",")
	if !ok {
		return "", ""
	}
	return pane, filepath.Base(path)
}

// OwnSessionID is the managed session the manager is itself running inside, or
// empty when it is running anywhere else -- outside tmux, on another server, or
// in a pane no session of ours holds.
//
// The name is asked of the server rather than read from $TMUX, which carries a
// numeric session id and names the session the client attached to rather than
// the one holding this pane.
func (d *Driver) OwnSessionID() string {
	pane, socket := OwnPane()
	if pane == "" || socket != d.socket {
		return ""
	}
	out, err := d.output(d.args("display-message", "-p", "-t", pane, "#{session_name}"))
	if err != nil {
		return ""
	}
	id, _ := SessionID(strings.TrimSpace(string(out)))
	return id
}

func (d *Driver) runningInOwnPane(target Target) bool {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || target.Name != pane {
		return false
	}
	// $TMUX is "<socket path>,<pid>,<session>"; the server is named by the
	// socket file, which is what -L takes.
	socket, _, ok := strings.Cut(os.Getenv("TMUX"), ",")
	if !ok {
		return false
	}
	return filepath.Base(socket) == resolveSocket(target.Socket)
}

// PrepareAttach restores automatic window sizing so the session fills the
// attaching client and tracks terminal resizes while attached. Without it,
// the manual size Resize pinned for the preview would leave the client's
// extra columns painted with tmux's out-of-bounds dotted overlay.
// "latest" needs tmux 3.1 (issue #114, Ubuntu 20.04 ships 3.0a); a server
// that rejects it gets "largest", which sizes to the single attaching
// client the same way. The rejection is the server's own verdict, so this
// stays correct when client binary and running server versions diverge.
//
// An adopted pane's window is pinned by Resize like any other, so it is
// restored like any other, on the server that holds it. Skipping it here
// would leave a person attaching to their own window stuck at the width of
// the manager's preview panel. Adoption also means the servers answering
// are no longer all one version, and the fallback verdict is remembered per
// driver rather than per server: one old server downgrades every session to
// "largest", which is the same size for the one client that is attaching.
func (d *Driver) PrepareAttach(id string) error {
	target := d.TargetName(id)
	if d.attachSizeLargest.Load() {
		_, err := d.runAt(id, "set-window-option", "-t", target, "window-size", "largest")
		return err
	}
	_, err := d.runAt(id, "set-window-option", "-t", target, "window-size", "latest")
	if err != nil && strings.Contains(err.Error(), "unknown value") {
		d.attachSizeLargest.Store(true)
		_, err = d.runAt(id, "set-window-option", "-t", target, "window-size", "largest")
	}
	return err
}

// Cursor reports where the session's caret sits in its visible pane, in
// cells from the top left. A capture carries no cursor, so a caller that
// has to tell an empty prompt from a half-written line asks tmux for it.
func (d *Driver) Cursor(id string) (int, int, error) {
	out, err := d.runAt(id, "display-message", "-p", "-t", d.TargetName(id), "#{cursor_x},#{cursor_y}")
	if err != nil {
		return 0, 0, err
	}
	column, row, ok := strings.Cut(strings.TrimSpace(out), ",")
	if !ok {
		return 0, 0, fmt.Errorf("tmux reported no cursor for session %s: %q", id, out)
	}
	x, err := strconv.Atoi(column)
	if err != nil {
		return 0, 0, fmt.Errorf("tmux cursor column %q for session %s: %w", column, id, err)
	}
	y, err := strconv.Atoi(row)
	if err != nil {
		return 0, 0, fmt.Errorf("tmux cursor row %q for session %s: %w", row, id, err)
	}
	return x, y, nil
}

// SessionInputAt reports when a client attached to the session last sent
// it a keystroke, and the zero time when none has. tmux stamps
// #{session_activity} from a client's key callback and from an attach:
// pane output, send-keys and paste-buffer all leave it alone, so a recent
// value means a person at a terminal, not the agent drawing or the manager
// typing. The stamp starts out equal to #{session_created}, which is not a
// keystroke and is told apart here. One-second resolution.
//
// A poll pass no longer calls this per session: the same stamps ride back on
// its batched capture (see CaptureState). It stays because the session tools
// run outside that pass and because the capture is allowed to carry no state.
func (d *Driver) SessionInputAt(id string) (time.Time, error) {
	out, err := d.runAt(id, "display-message", "-p", "-t", d.TargetName(id), "#{session_created} #{session_activity}")
	if err != nil {
		return time.Time{}, err
	}
	created, activity, ok := strings.Cut(strings.TrimSpace(out), " ")
	if !ok {
		return time.Time{}, fmt.Errorf("tmux reported no session activity for session %s: %q", id, out)
	}
	at, err := keystrokeAt(created, activity)
	if err != nil {
		return time.Time{}, fmt.Errorf("tmux session activity for session %s: %w", id, err)
	}
	return at, nil
}

// keystrokeAt turns tmux's #{session_created} and #{session_activity} pair
// into when a client last typed into the session, or the zero time when
// none has. One reader for both routes the pair arrives by -- this command
// and the capture chain's state line -- because two copies of the
// created-equals-activity rule would be two chances to read a fresh session
// as one somebody is sitting at.
func keystrokeAt(created, activity string) (time.Time, error) {
	if created == activity {
		return time.Time{}, nil
	}
	seconds, err := strconv.ParseInt(activity, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("session activity %q: %w", activity, err)
	}
	return time.Unix(seconds, 0), nil
}

// PaneStateAt reads one format from an arbitrary pane on a named server,
// for a pane that is not a session on the board at all.
//
// The manager's own pane is the case: it needs to know whether the operator
// is currently looking at it, and its pane is not something it adopted or
// created. The read is over the pooled client for that server where there is
// one, which on the operator's own server there always is.
func (d *Driver) PaneStateAt(socket, pane, format string) (string, error) {
	command := `display-message -p -t ` + pane + ` "` + format + `"`
	if control, err := d.captures.client(d, socket); err == nil && control != nil {
		if out, cmdErr := control.Command(command); cmdErr == nil {
			return out, nil
		}
	}
	out, err := d.combined([]string{"-L", socket, "display-message", "-p", "-t", pane, format})
	if err != nil {
		return "", fmt.Errorf("tmux pane state for %s on %s: %w: %s", pane, socket, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// PaneState reads one format from a session's pane on whichever server
// holds it, over the pooled pipe where there is one and by forking where
// there is not.
func (d *Driver) PaneState(id, format string) (string, error) {
	// Quoted: tmux's own parser reads a bare { as the start of a command
	// block and swallows the argument. The exec path hands the format over
	// as one argv entry and needs no quoting; the pipe is a command line.
	if out, ok := d.pipeCommand(id, `display-message -p -t `+d.TargetName(id)+` "`+format+`"`); ok {
		return out, nil
	}
	return d.runAt(id, "display-message", "-p", "-t", d.TargetName(id), format)
}

// paneProperty reads one format from a session's pane. list-panes accepts a
// pane id as its target but answers for every pane in that pane's window, so
// an adopted pane is asked about directly instead — otherwise a split window
// hands back a neighbour's pid or working directory.
func (d *Driver) paneProperty(id, format string) (string, error) {
	if target, adopted := d.AdoptedTarget(id); adopted {
		return d.runAt(id, "display-message", "-p", "-t", target.Name, format)
	}
	return d.run("list-panes", "-t", sessionName(id), "-F", format)
}

func (d *Driver) PanePID(id string) (int, error) {
	out, err := d.paneProperty(id, "#{pane_pid}")
	if err != nil {
		return 0, err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return 0, fmt.Errorf("no pane for session %s", id)
	}
	line = strings.SplitN(line, "\n", 2)[0]
	return strconv.Atoi(line)
}

// PaneCurrentPath is where the session's pane sits now, which follows any
// cd the shell or the agent made since launch, unlike the directory the
// session was created in.
func (d *Driver) PaneCurrentPath(id string) (string, error) {
	out, err := d.paneProperty(id, "#{pane_current_path}")
	if err != nil {
		return "", err
	}
	// Only the line break is stripped: a trailing space is part of a
	// directory name as much as any other character.
	line := strings.TrimSuffix(strings.SplitN(out, "\n", 2)[0], "\r")
	if line == "" {
		return "", fmt.Errorf("no pane for session %s", id)
	}
	return line, nil
}

// noServer recognizes every message tmux prints when a server has no session
// to answer a listing with. Two of them are the server being down: "no server
// running on <socket>" and, on Linux since 3.4, "error connecting to
// <socket> (No such file or directory)".
//
// The third is a server that is up and empty. tmux resolves a current session
// before it runs a listing, even one like "list-panes -a" that names no
// target and wants every pane on the server, so a server holding zero
// sessions fails that resolution and prints "no current target" rather than
// the empty listing it has. A manager sits on exactly that server between its
// last session exiting and tmux's own exit-empty shutdown, and it wants the
// answer it wants from a server that is down: nothing of mine is here. Read
// as a failure instead, it failed the whole liveness pass -- scanPanes makes
// one unreadable server fatal on purpose, because calling it empty would
// report every session on it gone.
//
// It cannot mask a real not-found. A server that holds sessions names the
// target it could not find ("can't find session: gi_1e261057"); only an empty
// one answers this way.
func noServer(out string) bool {
	return strings.Contains(out, "no server running") ||
		strings.Contains(out, "error connecting to") ||
		strings.Contains(out, "no current target")
}

// PaneScan is one liveness pass. PIDs is the map Panes returns: a session
// with a pane, and its pane pid. Gone is the narrower question a caller has
// to answer before it removes anything — which adopted sessions a server
// answered for and did not name.
//
// The two are not complements. A session absent from PIDs may be gone, or may
// live on a server this pass could not read; Gone holds only the first kind.
type PaneScan struct {
	PIDs map[string]int
	// Gone is the adopted sessions whose pane is proven no longer there:
	// their server produced a pane listing, and the pane was not on it. A
	// server that could not be listed at all fails the whole scan rather
	// than quietly emptying this map, so an entry here is always evidence
	// rather than the absence of it.
	Gone map[string]bool
}

// Panes returns every session's pane pid, which doubles as a liveness check:
// a session absent from the map has no pane to show. Managed sessions come
// back in a single tmux call; adopted ones cost one listing per foreign
// server. Absent is good enough to read a status off and not good enough to
// delete on — see ScanPanes.
func (d *Driver) Panes() (map[string]int, error) {
	scan, err := d.ScanPanes()
	return scan.PIDs, err
}

// ScanTimeout bounds one whole scan -- the managed listing and every adopted
// server's, together -- because the poll pass this runs in has to end whether
// or not tmux answers. The budget is shared rather than per-call so a board
// with adopted servers on it cannot multiply the stall by the number of them.
//
// Four seconds is the line the measured scans draw themselves. A scan takes
// 5-26ms (p50 7ms) on an idle board; the six slow passes in the rotated logs
// that scan dominated ran 0.8s, 2.0s, 2.1s and 3.1s -- a busy server, still
// answering -- and then 6.4s and 10.8s, which are the two this exists to cap.
// Above everything that answered, below both that did not, and two poll
// intervals, by which point the board is showing stale rows either way.
//
// A pass killed here does no work at all, so the budget is deliberately not
// tuned down towards the interval: a deadline that fires on a server that was
// about to answer costs a whole pass to save a fraction of one.
//
// A scan that overruns it fails. It does not come back short: see serverPanes.
const ScanTimeout = 4 * time.Second

// ScanPanes is Panes with the evidence kept: see PaneScan. A caller that only
// wants liveness wants Panes; a caller about to delete something wants this.
func (d *Driver) ScanPanes() (PaneScan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ScanTimeout)
	defer cancel()
	return d.scanPanes(ctx)
}

func (d *Driver) scanPanes(ctx context.Context) (PaneScan, error) {
	out, err := d.combinedWithin(ctx, d.args("list-panes", "-a", "-F", "#{session_name} #{pane_pid}"))
	// A deadline that fired is not an answer about this server, so it is
	// checked ahead of the no-server reading below: that one treats silence
	// as "no sessions here", which is the reading a timeout must never get.
	if errors.Is(err, ErrTimeout) {
		return PaneScan{}, fmt.Errorf("tmux list-panes: %w", err)
	}
	// Our own server not being up is not the end of the answer: a manager
	// holding only adopted sessions never starts one, and returning here left
	// every one of those sessions absent from the map, which reads as dead.
	if err != nil && !noServer(string(out)) {
		return PaneScan{}, fmt.Errorf("tmux list-panes: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err != nil {
		out = nil
	}
	scan := PaneScan{PIDs: map[string]int{}, Gone: map[string]bool{}}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, pidText, ok := strings.Cut(line, " ")
		if !ok || !managedName(name) {
			continue
		}
		id := strings.TrimPrefix(name, prefix)
		if _, taken := scan.PIDs[id]; taken {
			continue
		}
		if pid, err := strconv.Atoi(pidText); err == nil {
			scan.PIDs[id] = pid
		}
	}
	if err := d.adoptedPanes(ctx, scan); err != nil {
		return PaneScan{}, err
	}
	return scan, nil
}

// adoptedPanes adds the adopted sessions to the liveness map. They carry no
// gi_ name and mostly sit on another server, so the listing above cannot see
// them at all — and a session missing from that map reads as dead. A pane
// that has since closed is absent from the pids and named in Gone, which is
// the pair of answers wanted.
//
// A listing that fails fails the scan. Reporting it as an empty server would
// make every session on it look gone, and something downstream deletes rows
// on that word.
func (d *Driver) adoptedPanes(ctx context.Context, scan PaneScan) error {
	d.adoptedMu.RLock()
	targets := make(map[string]Target, len(d.adopted))
	for id, target := range d.adopted {
		targets[id] = target
	}
	d.adoptedMu.RUnlock()
	byServer := map[string]map[string]int{}
	for id, target := range targets {
		live, listed := byServer[target.Socket]
		if !listed {
			var err error
			live, err = d.serverPanes(ctx, target.Socket)
			if err != nil {
				return err
			}
			byServer[target.Socket] = live
		}
		if pid, ok := live[target.Name]; ok {
			scan.PIDs[id] = pid
			continue
		}
		scan.Gone[id] = true
	}
	return nil
}

// serverPanes maps every pane id, and every session name, on one server to a
// pane pid. Both are indexed because a Target may name either.
//
// A server that is not running answers with no panes rather than an error,
// and that is a finding, not a shrug: a pane cannot outlive the server it
// lives on. Holding a row open for one would be worse than dropping it, since
// pane ids restart at %0 on a fresh server and the row would then address
// somebody else's pane.
//
// That reading is exactly why the deadline is checked before it. A killed
// tmux prints nothing, and nothing is not "no server running" -- read as one,
// every adopted session on the socket would land in Gone and be deleted on a
// pass that never reached the server at all.
func (d *Driver) serverPanes(ctx context.Context, socket string) (map[string]int, error) {
	out, err := d.combinedWithin(ctx, []string{"-L", socket, "list-panes", "-a", "-F", "#{pane_id} #{pane_pid} #{session_name}"})
	if errors.Is(err, ErrTimeout) {
		return nil, fmt.Errorf("tmux list-panes on %s: %w", socket, err)
	}
	if err != nil {
		if noServer(string(out)) {
			return map[string]int{}, nil
		}
		return nil, fmt.Errorf("tmux list-panes on %s: %w: %s", socket, err, strings.TrimSpace(string(out)))
	}
	live := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.SplitN(line, " ", 3)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		live[fields[0]] = pid
		// A session-named target answers with its first pane, matching what
		// list-panes -F gives a managed session.
		if _, taken := live[fields[2]]; !taken {
			live[fields[2]] = pid
		}
	}
	return live, nil
}
