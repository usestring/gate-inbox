package hooks

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/usestring/gate-inbox/internal/singleton"
)

// Arrivals.
//
// The board finds a pane somebody started by hand with a scan that runs
// every 45 seconds, so a session started outside the board used to appear a
// minute late, and one whose process did not look like an agent never
// appeared at all. An agent can say so itself instead: the global
// SessionStart hook, which every claude on the machine runs as it starts,
// drops a file under hooks/arrivals/ naming the pane it runs in, and the
// board, which watches that directory, scans just that pane at once.
//
// The hook does it with shell builtins and only when the board is running
// (the pid in its singleton lock answers kill -0) and has no marker for the
// pane yet, so a session nowhere near a running board writes nothing and
// starts no process. The file is named like a marker, for the server's pid
// and the pane id, so a pane that announces itself twice leaves one file,
// and holds $TMUX, $TMUX_PANE and the agent's pid, one per line.

// arrivalsDirName holds the announcements the board has not read yet.
const arrivalsDirName = "arrivals"

// arrivalEvent is the one global event that announces a pane: it fires as
// a session starts, which is the moment the board wants to hear about it.
const arrivalEvent = "SessionStart"

// arrivalPartialGrace is how long a file that does not parse is left for
// its writer to finish before it is thrown away. The hook writes with a
// plain redirect, which is not atomic.
const arrivalPartialGrace = 5 * time.Second

// ArrivalsDir is where announcements land for the board to read.
func (m *Manager) ArrivalsDir() string {
	return filepath.Join(m.dir, arrivalsDirName)
}

// PrepareArrivals makes the directory the hooks write into. A hook never
// creates it (mkdir is not a builtin), so until the board has run once,
// announcing is a redirect that fails in silence.
func (m *Manager) PrepareArrivals() error {
	return os.MkdirAll(m.ArrivalsDir(), 0o755)
}

// Arrival is one agent that announced the pane it runs in.
type Arrival struct {
	// Socket is the -L name of the tmux server, read off $TMUX's path.
	Socket string
	// ServerPID is that server's pid, the second field of $TMUX. A pane id
	// is only unique on one server, and a server restarted on the same
	// socket numbers its panes from %0 again, so a scan checks it.
	ServerPID int
	PaneID    string
	// AgentPID is the process that announced itself: the hook's parent,
	// which is the agent. A pane whose process tree no longer holds it is
	// not the pane that announced.
	AgentPID int
}

// parseTmuxEnv splits $TMUX ("<socket path>,<server pid>,<session>") into
// the socket's -L name and the server's pid.
func parseTmuxEnv(tmuxEnv string) (socket string, serverPID int, ok bool) {
	path, rest, found := strings.Cut(tmuxEnv, ",")
	if !found || path == "" {
		return "", 0, false
	}
	server, _, _ := strings.Cut(rest, ",")
	pid, err := strconv.Atoi(server)
	if err != nil || pid <= 0 {
		return "", 0, false
	}
	socket = filepath.Base(path)
	if socket == "" || socket == "." || socket == string(filepath.Separator) {
		return "", 0, false
	}
	return socket, pid, true
}

// ParseArrival reads one announcement file's content.
func ParseArrival(raw string) (Arrival, bool) {
	if !strings.HasSuffix(raw, "\n") {
		return Arrival{}, false
	}
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if len(lines) != 3 || !paneIDPattern.MatchString(lines[1]) {
		return Arrival{}, false
	}
	socket, server, ok := parseTmuxEnv(lines[0])
	if !ok {
		return Arrival{}, false
	}
	agent, err := strconv.Atoi(lines[2])
	if err != nil || agent <= 0 {
		return Arrival{}, false
	}
	return Arrival{Socket: socket, ServerPID: server, PaneID: lines[1], AgentPID: agent}, true
}

// TakeArrivals reads and removes every announcement waiting. One still
// being written is left for the next call, unless it has been unreadable
// for longer than any write takes, when it is thrown away. A directory that
// does not exist holds nothing.
func (m *Manager) TakeArrivals(now time.Time) []Arrival {
	dir := m.ArrivalsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var arrivals []Arrival
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		arrival, ok := ParseArrival(string(raw))
		if !ok {
			if info, err := entry.Info(); err == nil && now.Sub(info.ModTime()) < arrivalPartialGrace {
				continue
			}
		}
		_ = removeIfExists(path)
		if ok {
			arrivals = append(arrivals, arrival)
		}
	}
	return arrivals
}

// Announce is the hook's announcement for a caller that is already running
// as the agent's child -- the MCP relay, which every claude starts with it
// -- and so costs no process to make. It writes only what the hook would:
// nothing for a session the board launched, one outside tmux, a pane that
// already has a marker, or when the board is not running.
func (m *Manager) Announce(tmuxEnv, paneID string, agentPID int) bool {
	if os.Getenv(EnvStatusFile) != "" || agentPID <= 0 || !paneIDPattern.MatchString(paneID) {
		return false
	}
	_, server, ok := parseTmuxEnv(tmuxEnv)
	if !ok {
		return false
	}
	name := strconv.Itoa(server) + paneID
	if _, err := os.Stat(filepath.Join(m.AdoptedDir(), name)); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if !boardRunning(m.root) {
		return false
	}
	content := tmuxEnv + "\n" + paneID + "\n" + strconv.Itoa(agentPID) + "\n"
	return WriteWhole(filepath.Join(m.ArrivalsDir(), name), content) == nil
}

// boardRunning is the hook's kill -0 on the singleton lock's pid.
func boardRunning(configDir string) bool {
	raw, err := os.ReadFile(filepath.Join(configDir, singleton.FileName))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0]))
	if err != nil || pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
