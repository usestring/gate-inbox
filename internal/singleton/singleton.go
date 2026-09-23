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
	PID    int
	Killed bool // SIGTERM was not enough; SIGKILL was sent.
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
	_ = syscall.Kill(pid, syscall.SIGTERM)
	if waitLock(file, termGrace) {
		return finish(file, took)
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
		return finish(file, took)
	}
	file.Close()
	return nil, took, fmt.Errorf("manager pid %d holds %s and would not exit", pid, path)
}

func finish(file *os.File, took *Takeover) (*Lock, *Takeover, error) {
	lock, _, err := claim(file)
	return lock, took, err
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
