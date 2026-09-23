// Package managerbuild answers one question: is the manager binary serving
// this session the same one the board is running?
//
// It is worth a package because the answer is invisible everywhere else. A
// session's MCP server is started once, by its CLI, and then lives as long
// as the conversation does -- weeks. The board restarts many times over
// those weeks, and nothing tells the old server it has been left behind: it
// keeps offering the tool descriptions and arguments of whatever build
// started it, so a tool that has since gained an argument silently does
// less than the current one would, and no log line anywhere says why.
//
// A session cannot fix this for itself -- only the person can reconnect the
// server -- so the least it can do is say so.
package managerbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// FileName is the board's record, under the manager's config directory.
const FileName = "manager-build"

var self struct {
	sync.Once
	digest string
}

// Fingerprint is the sha256 of the running binary, or "" when it cannot be
// read. Memoised because a process cannot change its own executable.
//
// On Linux it reads /proc/self/exe rather than the path os.Executable
// reports, and that is the whole reason this works here: the board runs
// under `go run`, whose binary is deleted when the run exits, so by the
// time an old MCP server asks this question its own path is long gone.
// /proc/self/exe still opens the inode the process is executing.
func Fingerprint() string {
	self.Do(func() { self.digest = digestSelf() })
	return self.digest
}

func digestSelf() string {
	paths := []string{}
	if runtime.GOOS == "linux" {
		paths = append(paths, "/proc/self/exe")
	}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, exe)
	}
	for _, path := range paths {
		if sum := digestFile(path); sum != "" {
			return sum
		}
	}
	return ""
}

func digestFile(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return ""
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// Record notes the running binary as the board's current one, in dir. The
// timestamp is when this fingerprint FIRST appeared, not when the board
// last started: a board that restarts hourly without being rebuilt must not
// keep resetting the clock, or a session that has been behind for a week
// would report being behind for an hour.
func Record(dir string) error {
	digest := Fingerprint()
	if digest == "" {
		return nil
	}
	if current, _, ok := read(dir); ok && current == digest {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	line := digest + " " + time.Now().UTC().Format(time.RFC3339) + "\n"
	return os.WriteFile(filepath.Join(dir, FileName), []byte(line), 0o644)
}

// StaleSince reports how long the board has been running a build other than
// this process's, and whether it is behind at all. Everything unknown reads
// as current: a missing record, an unreadable binary, a board that has not
// started since this file shipped. Warning a session that might be fine is
// worse than staying quiet, because the warning it cannot act on is the one
// it learns to ignore.
func StaleSince(dir string, now time.Time) (time.Duration, bool) {
	// The record is read first so a session on a board that has never
	// written one -- every session until this ships -- never pays to hash
	// 23MB of its own binary on the way to answering "no".
	board, since, ok := read(dir)
	if !ok {
		return 0, false
	}
	mine := Fingerprint()
	if mine == "" || board == mine {
		return 0, false
	}
	behind := now.Sub(since)
	if behind < 0 {
		behind = 0
	}
	return behind, true
}

func read(dir string) (string, time.Time, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return "", time.Time{}, false
	}
	digest, stamp, found := strings.Cut(strings.TrimSpace(string(raw)), " ")
	if digest == "" || !found {
		return "", time.Time{}, false
	}
	since, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp))
	if err != nil {
		return "", time.Time{}, false
	}
	return digest, since, true
}
