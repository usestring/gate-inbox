package compat

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// legacyName matches the names the board carried before it became Gate
// Inbox: the command, its env prefixes, and the tmux session prefix. The
// public release refuses a tree that still carries them, this file
// included, so the pattern is built from pieces rather than spelled out.
var legacyName = regexp.MustCompile(strings.Join([]string{
	"agent" + "-manager",
	"Agent" + " Manager",
	"AGENT" + "_MANAGER",
	`\bAM` + `_[A-Z]`,
	`\bam` + `_`,
}, "|"))

// TestNoLegacyNames fails when an input or a recorded golden names the
// board as it was before the rename.
func TestNoLegacyNames(t *testing.T) {
	err := filepath.WalkDir("testdata", func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if found := legacyName.FindString(line); found != "" {
				t.Errorf("%s:%d names %q", path, i+1, found)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
