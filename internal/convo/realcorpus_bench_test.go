package convo

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// realHome is the operator's own Claude Code home. These measurements are
// worth nothing against a fixture: the cost being chased is a function of how
// many transcripts and project directories have piled up on a real machine,
// and a synthetic corpus has whatever shape the test author guessed.
func realHome(tb testing.TB) (string, string) {
	tb.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		tb.Skip("no home directory")
	}
	claude := filepath.Join(home, ".claude")
	if _, err := os.Stat(filepath.Join(claude, "projects")); err != nil {
		tb.Skip("no Claude Code corpus on this machine")
	}
	opencode := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if _, err := os.Stat(opencode); err != nil {
		opencode = ""
	}
	return claude, opencode
}

// realDirs are the working directories a sweep would ask about: one per
// distinct session cwd on the board. The repos tree stands in for the board
// here, which is the same order of magnitude and needs no state.db.
func realDirs(tb testing.TB) []string {
	tb.Helper()
	home, _ := os.UserHomeDir()
	var dirs []string
	for _, root := range []string{filepath.Join(home, "repos"), filepath.Join(home, "repos", "worktrees")} {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(root, e.Name()))
			}
		}
	}
	return dirs
}

// TestRealCorpusRefreshCost is a measurement, not an assertion, and only runs
// when asked. It prints the phase breakdown a sweep pays on this machine.
func TestRealCorpusRefreshCost(t *testing.T) {
	if os.Getenv("CONVO_MEASURE") == "" {
		t.Skip("set CONVO_MEASURE=1 to measure the real corpus")
	}
	claude, opencode := realHome(t)
	dirs := realDirs(t)
	ix := New(claude, opencode)

	for pass := 1; pass <= 3; pass++ {
		start := time.Now()
		if err := ix.Refresh(dirs); err != nil {
			t.Logf("pass %d: refresh error (still timed): %v", pass, err)
		}
		c := ix.Cost()
		fmt.Printf("pass %d  total=%-9s sidecars=%-9s paths=%-9s tails=%-9s opencode=%-9s files=%d cached=%d bytes=%dMB convos=%d dirs=%d wall=%s\n",
			pass,
			c.Elapsed.Round(time.Millisecond),
			c.Sidecars.Round(time.Millisecond),
			c.Paths.Round(time.Millisecond),
			c.Tails.Round(time.Millisecond),
			c.OpenCode.Round(time.Millisecond),
			c.Files, c.Cached, c.Bytes/(1<<20),
			len(ix.Conversations()), len(dirs),
			time.Since(start).Round(time.Millisecond))
	}
}
