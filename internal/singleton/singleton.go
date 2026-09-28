// Package singleton keeps one interactive manager per config directory. A
// second board polling the same tmux servers pins windows twice, races the
// first over the store, and leaves the operator with two panes claiming to
// own every session -- so the newer start wins and the incumbent is asked to
// leave, the way a fresh login supersedes a stale one.
package singleton

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// FileName is the lock file under the config directory. Its content is the
// holder's pid; the lock itself is an flock, which the kernel drops with the
// holder, so a manager killed with SIGKILL never leaves a stale claim.
const FileName = "manager.lock"

// PaneFileName records the holder's $TMUX and $TMUX_PANE, one per line, so a
// successor can close the pane it was drawn in. It is kept out of the lock
// file because older builds read that file as nothing but a pid.
const PaneFileName = "manager.pane"

const (
	termGrace = 5 * time.Second
	killGrace = 2 * time.Second
	pollEvery = 50 * time.Millisecond
)

// Lock is the held claim; Release hands it back.
type Lock struct {
	file *os.File
}

// Takeover records what happened to an incumbent so the caller can log it.
type Takeover struct {
	PID        int
	Killed     bool // SIGTERM was not enough; SIGKILL was sent.
	PaneClosed bool // The tmux pane the incumbent was drawn in was killed.
}

// pane is where a holder's board was drawn, as tmux told it through the
// environment. The server pid is kept because a restarted server on the same
// socket numbers its panes from %0 again.
type pane struct {
	socket    string
	serverPID string
	id        string
}

// Acquire claims dir for this process. When another manager holds the claim
// it is sent SIGTERM -- Bubble Tea turns that into a clean quit that restores
// its pins -- and, failing that within a grace period, SIGKILL. The returned
// Takeover is nil when nobody was in the way.
func Acquire(dir string) (*Lock, *Takeover, error) {
	path := filepath.Join(dir, FileName)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, err
	}
	if tryLock(file) == nil {
		return claim(file)
	}

	pid := holderPID(file)
	if pid <= 0 || pid == os.Getpid() {
		// Held, but by nobody we can name: wait out the grace period in
		// case the holder is on its way down, then give up.
		if waitLock(file, termGrace) {
			return claim(file)
		}
		file.Close()
		return nil, nil, fmt.Errorf("another manager holds %s and names no pid", path)
	}

	took := &Takeover{PID: pid}
	where := holderPane(dir)
	_ = syscall.Kill(pid, syscall.SIGTERM)
	if waitLock(file, termGrace) {
		return finish(file, took, where)
	}
	// A concurrent start may have taken the claim while this one waited;
	// its pid is not the one that ignored SIGTERM, and the old one may
	// already belong to some other process.
	if now := holderPID(file); now != pid {
		file.Close()
		return nil, took, fmt.Errorf("manager pid %d took %s while pid %d was being superseded", now, path, pid)
	}
	took.Killed = true
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if waitLock(file, killGrace) {
		return finish(file, took, where)
	}
	file.Close()
	return nil, took, fmt.Errorf("manager pid %d holds %s and would not exit", pid, path)
}

func finish(file *os.File, took *Takeover, where pane) (*Lock, *Takeover, error) {
	lock, _, err := claim(file)
	if err == nil && where.id != "" {
		// The lock is released before the incumbent's other defers restore
		// its pins, and killing the pane hangs it up mid-restore, so the pane
		// waits for the process.
		waitExit(took.PID, termGrace)
		took.PaneClosed = closePane(where)
	}
	return lock, took, err
}

// closePane kills the pane a superseded board was drawn in, so a relaunch
// leaves one board on screen rather than a board and the shell the old one
// exited to. It refuses a pane it cannot be sure is still that one: its own,
// or one on a server that has restarted since the holder wrote it down.
func closePane(where pane) bool {
	if where.socket == "" || where.id == "" {
		return false
	}
	if where.id == os.Getenv("TMUX_PANE") && where.socket == socketOf(os.Getenv("TMUX")) {
		return false
	}
	out, err := exec.Command("tmux", "-S", where.socket, "display-message", "-p", "-t", where.id, "#{pid}").Output()
	if err != nil || strings.TrimSpace(string(out)) != where.serverPID {
		return false
	}
	return exec.Command("tmux", "-S", where.socket, "kill-pane", "-t", where.id).Run() == nil
}

func waitExit(pid int, limit time.Duration) {
	deadline := time.Now().Add(limit)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(pollEvery)
	}
}

func claim(file *os.File) (*Lock, *Takeover, error) {
	if err := file.Truncate(0); err != nil {
		file.Close()
		return nil, nil, err
	}
	if _, err := file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		file.Close()
		return nil, nil, err
	}
	// Written even outside tmux, so a successor never reads the pane of a
	// holder before this one. Losing it costs only the pane close.
	record := os.Getenv("TMUX") + "\n" + os.Getenv("TMUX_PANE") + "\n"
	_ = os.WriteFile(filepath.Join(filepath.Dir(file.Name()), PaneFileName), []byte(record), 0o644)
	return &Lock{file: file}, nil, nil
}

// Release drops the claim. Safe on a nil Lock.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	l.file.Close()
	l.file = nil
}

func tryLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func waitLock(file *os.File, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for {
		err := tryLock(file)
		if err == nil {
			return true
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return false
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollEvery)
	}
}

func holderPID(file *os.File) int {
	buf := make([]byte, 32)
	n, _ := file.ReadAt(buf, 0)
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		return 0
	}
	return pid
}

// holderPane is empty for a holder outside tmux, and for one started by a
// build that did not record its pane.
func holderPane(dir string) pane {
	data, err := os.ReadFile(filepath.Join(dir, PaneFileName))
	if err != nil {
		return pane{}
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 {
		return pane{}
	}
	fields := strings.Split(strings.TrimSpace(lines[0]), ",")
	if len(fields) < 2 {
		return pane{}
	}
	return pane{socket: fields[0], serverPID: fields[1], id: strings.TrimSpace(lines[1])}
}

// socketOf is the socket path in a $TMUX value.
func socketOf(tmuxEnv string) string {
	return strings.Split(tmuxEnv, ",")[0]
}
