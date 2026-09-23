package launch

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/usestring/gate-inbox/internal/config"
)

// stableExeDir and stableExeName locate the copy of the manager that
// spawned sessions are pointed at, under the manager's own home.
const (
	stableExeDir  = "bin"
	stableExeName = "gate-inbox"
)

// Executable names the binary that generated MCP configs and
// $GATE_INBOX_BIN point at.
//
// Not os.Executable(): the board runs under `go run`, whose binary is
// deleted when that run exits, so a session it spawned holds a
// --mcp-config naming a path that is already gone and a
// $GATE_INBOX_BIN that cannot be executed -- for the life of the
// session, which is weeks across many board restarts. A session whose MCP
// server is still alive is also still running that run's CODE, so an
// argument a tool gained since never reaches its agent.
//
// This reads the installed path and never writes it. Only the board
// installs: a weeks-old MCP server calls this on every spawn, and letting
// it install would overwrite a current copy with its own stale one.
//
// Not memoised -- one Stat per spawn, and a cached answer would depend on
// whether the board happened to ask before or after Install.
func Executable() string {
	if dir, err := config.Dir(); err == nil {
		if path := installedExecutable(dir); path != "" {
			return path
		}
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return stableExeName
}

// Install copies the running binary to the path Executable names and
// returns it. The board calls it once at startup, before it spawns
// anything -- and, just as importantly, on behalf of the sessions an
// EARLIER run spawned: reconnecting one of their MCP servers re-execs this
// path, so refreshing it here is what lets a weeks-old session reach
// current code without being restarted.
//
// It never fails the caller. A home that cannot be written is a board that
// spawns exactly what it spawned before.
func Install() string {
	dir, err := config.Dir()
	if err != nil {
		return Executable()
	}
	exe, err := os.Executable()
	if err != nil {
		return Executable()
	}
	if path := installExecutable(exe, dir); path != "" {
		return path
	}
	return Executable()
}

// Installed names the copy under the manager's home, or "" when there is
// none. Executable falls back to os.Executable, which is the right answer for
// "what path do I write into a config" and the wrong one for "is there a
// binary here other than me": a caller that wants to hand work to current
// code must not be handed itself.
func Installed() string {
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	return installedExecutable(dir)
}

// installedExecutable names dir's copy when there is a usable one there.
func installedExecutable(dir string) string {
	target := filepath.Join(dir, stableExeDir, stableExeName)
	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return target
	}
	return ""
}

// installExecutable puts exe at dir/bin/gate-inbox and returns that path, or "" when there is nothing usable to name. exe
// may be empty, or may have been deleted out from under a running board --
// both are the `go run` case one restart later -- and a copy an earlier run
// left is closer to current than a path that no longer resolves.
func installExecutable(exe, dir string) string {
	target := filepath.Join(dir, stableExeDir, stableExeName)
	installed := false
	if exe != "" {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved == target {
			installed = true
		} else if err := installCopy(exe, target); err == nil {
			installed = true
		}
	}
	if installed {
		return target
	}
	return installedExecutable(dir)
}

// installCopy replaces target with the bytes of src through a rename, so a
// session launching in the same moment either execs the whole old binary or
// the whole new one. Processes already running the old file keep the inode
// they opened; nothing is pulled out from under them.
func installCopy(src, target string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("manager binary is not a regular file: " + src)
	}
	if unchanged(source, info.Size(), target) {
		return nil
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	staged, err := os.CreateTemp(filepath.Dir(target), stableExeName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	if _, err := io.Copy(staged, source); err != nil {
		staged.Close()
		return err
	}
	if err := staged.Chmod(0o755); err != nil {
		staged.Close()
		return err
	}
	// Durable before it is reachable: the rename is what publishes this
	// file, and a crash between the two would leave every spawn for the
	// rest of the day exec'ing a truncated binary.
	if err := staged.Sync(); err != nil {
		staged.Close()
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	return os.Rename(staged.Name(), target)
}

// unchanged reports whether target already holds src's bytes, so a board
// restart that rebuilt nothing does not rewrite 23MB and does not give the
// file a new inode for no reason.
func unchanged(source io.Reader, size int64, target string) bool {
	info, err := os.Stat(target)
	if err != nil || info.Size() != size {
		return false
	}
	existing, err := os.Open(target)
	if err != nil {
		return false
	}
	defer existing.Close()
	want, ok := digest(source)
	if !ok {
		return false
	}
	got, ok := digest(existing)
	// A read that failed must never read as a match: two unreadable files
	// would otherwise agree, and the copy would be skipped forever.
	return ok && got == want
}

func digest(r io.Reader) (string, bool) {
	sum := sha256.New()
	if _, err := io.Copy(sum, r); err != nil {
		return "", false
	}
	return string(sum.Sum(nil)), true
}
