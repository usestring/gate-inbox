//go:build unix

package app

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/usestring/gate-inbox/internal/logging"
)

// StderrFileName is the file that takes the board's fd 2 while it runs.
const StderrFileName = "board-stderr.log"

// stderrMaxBytes caps the file: past it, the next board start truncates it.
// A variable so the test can exercise truncation without a 10 MiB write.
var stderrMaxBytes int64 = 10 << 20

// redirectStderr points file descriptor 2 at dir/board-stderr.log and returns
// a function that puts the original back. Writes made through fd 2 by the
// runtime, cgo or system libraries (such as libmalloc's warnings) go to the
// file instead of drawing over the frame. On any failure it leaves fd 2
// alone and returns a no-op: a noisy board beats one that refuses to start.
func redirectStderr(dir string) func() {
	noop := func() {}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logging.Warn("stderr stays on the terminal: no log directory", logging.Err(err))
		return noop
	}
	path := filepath.Join(dir, StderrFileName)
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > stderrMaxBytes {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		logging.Warn("stderr stays on the terminal: cannot open its file", "path", path, logging.Err(err))
		return noop
	}
	saved, err := unix.Dup(2)
	if err != nil {
		file.Close()
		logging.Warn("stderr stays on the terminal: cannot save fd 2", logging.Err(err))
		return noop
	}
	if err := unix.Dup2(int(file.Fd()), 2); err != nil {
		unix.Close(saved)
		file.Close()
		logging.Warn("stderr stays on the terminal: cannot redirect fd 2", logging.Err(err))
		return noop
	}
	file.Close()
	logging.Info("stderr redirected", "path", path)
	return func() {
		if err := unix.Dup2(saved, 2); err != nil {
			logging.Warn("could not restore stderr", logging.Err(err))
		}
		unix.Close(saved)
	}
}
