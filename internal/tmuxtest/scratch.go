package tmuxtest

import (
	"os"
	"testing"
)

// ScratchDir is where a test's sqlite file goes: memory-backed storage when
// the machine has it, the ordinary temp dir otherwise.
//
// Opening a store writes a WAL and commits a schema, and each of those commits
// is an fsync. Measured on the development host, under the load a board of agents puts on
// it: 1.7 seconds per open on disk against 9 milliseconds on tmpfs -- and
// internal/ui alone opens one in nearly five hundred tests, which is most of
// what a full run of that package used to spend its time doing.
//
// It is for tests whose subject is the data, not the disk. A test that asserts
// a commit survives the machine losing power wants a real file and should say
// so by asking for one.
func ScratchDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("/dev/shm", "gate-inbox-test")
	if err != nil {
		// No /dev/shm, or no room in it: the ordinary temp dir is slower,
		// never wrong.
		return t.TempDir()
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
