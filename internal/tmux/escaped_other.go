//go:build !linux

package tmux

// readEscapedProcs has no portable way to read another process's
// environment, so off Linux the escaped pass finds nothing and teardown
// reaps the window tree alone.
func readEscapedProcs() []escapedProc { return nil }

func startOf(int) (uint64, bool) { return 0, false }

func parentOf(int) (int, bool) { return 0, false }
