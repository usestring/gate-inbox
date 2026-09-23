package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every fixture in this module has to mean the same thing in every checkout.
// A recorded one can capture something belonging to the run that produced it
// instead of to the thing under test, and then it matches only where it was
// written: the fleet golden once held the absolute path of the worktree that
// recorded it and failed everywhere else, main included.
//
// This looks for the two kinds that cannot be deliberate: a path inside the
// checkout doing the walking, and a value no rerun reproduces. It is narrow
// on purpose. A fixture may hold an absolute path that is simply written
// down -- internal/ui/fleet_bench_test.go gives its groups a fixed
// /home/user/repos/sample-repo precisely so the golden reads the same
// everywhere -- and that is the fix, not the bug.
func TestCommittedFixturesCarryNothingFromTheRunThatRecordedThem(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	files := fixtureFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no fixture files found to check")
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		name, _ := filepath.Rel(root, path)
		if strings.Contains(body, root) {
			t.Errorf("%s carries the checkout it was recorded in (%s): a fixture holding the "+
				"path of the tree that produced it only matches in that tree", name, root)
		}
		if found := tempDirPattern().FindString(body); found != "" {
			t.Errorf("%s carries a per-test temporary directory (%q), which no rerun reproduces",
				name, found)
		}
		for _, found := range uuidPattern().FindAllString(body, -1) {
			if writtenByHand(found) {
				continue
			}
			t.Errorf("%s carries a generated UUID (%q), which no rerun reproduces", name, found)
		}
	}
}

// tempDirPattern is the per-test directory the testing package hands out, and
// uuidPattern the random socket and session names the tmux tests build so
// that two checkouts running at once cannot kill each other's servers.
func tempDirPattern() *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(os.TempDir()) + `/Test\w+\d{3,}`)
}

func uuidPattern() *regexp.Regexp {
	return regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
}

// writtenByHand tells a placeholder like 11111111-2222-3333-4444-555555555555
// from something uuid.NewString() produced. A person spelling an id out fills
// each group with one repeated character; a generated one never does.
func writtenByHand(id string) bool {
	for _, group := range strings.Split(id, "-") {
		if strings.Trim(group, group[:1]) != "" {
			return false
		}
	}
	return true
}

// fixtureFiles is everything under a testdata directory, which is where the
// go tool expects fixtures to live and so where a recorded one lands.
func fixtureFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "testdata" ||
			strings.Contains(path, string(os.PathSeparator)+"testdata"+string(os.PathSeparator)) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}
