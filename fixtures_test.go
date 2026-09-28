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
		for _, found := range uuidsIn(body) {
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
	return regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
}

// uuidsIn is every UUID in body that stands on its own. Its edges are checked
// by hand rather than with \b because Go counts an underscore as a word
// character: a recorded id usually arrives behind a prefix such as fco_ or
// call_, and \b sees no boundary between that underscore and the hex after
// it, so the id would pass unseen. What has to be ruled out instead is a
// longer run of hex and dashes that merely contains the shape, which is
// something else and not a UUID at all. The neighbours are read from the body
// rather than matched, so two ids one separator apart are both found.
func uuidsIn(body string) []string {
	var found []string
	for _, span := range uuidPattern().FindAllStringIndex(body, -1) {
		start, end := span[0], span[1]
		if start > 0 && continuesHexRun(body[start-1]) {
			continue
		}
		if end < len(body) && continuesHexRun(body[end]) {
			continue
		}
		found = append(found, body[start:end])
	}
	return found
}

// continuesHexRun reports whether a neighbouring byte would make a match part
// of a longer run of hex and dashes instead of a UUID of its own.
func continuesHexRun(c byte) bool {
	return c == '-' || '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
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

// The committed-fixture test only fails once a fixture is dirty, so the
// matching it relies on is pinned here directly: an id behind a prefix has to
// be caught, and a longer hex run has to be left alone.
func TestUUIDsInFindsIDsBehindAPrefix(t *testing.T) {
	const id = "7c3e9a52-4b1d-4f86-a0e2-93d5c6b7f814"
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"bare", `"id":"` + id + `"`, []string{id}},
		{"whole body", id, []string{id}},
		{"fco prefix", `"id":"fco_` + id + `"`, []string{id}},
		{"call prefix", "call_" + id, []string{id}},
		{"run prefix", "run_" + id, []string{id}},
		{"sess prefix", "sess_" + id, []string{id}},
		{"two one separator apart", id + "," + id, []string{id, id}},
		{"longer leading hex", "a" + id, nil},
		{"longer trailing hex", id + "0", nil},
		{"extra trailing group", id + "-beef", nil},
		{"extra leading group", "beef-" + id, nil},
		{"no dashes", "fc_0da31f31e9c7d263016aa0e0fe6cd487d2958fc1ee0ca2b709", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := uuidsIn(tc.body)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("uuidsIn(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

// A placeholder behind a prefix is still a placeholder: excusing it depends on
// the id alone, not on what sits in front of it.
func TestWrittenByHandExcusesAPrefixedPlaceholder(t *testing.T) {
	found := uuidsIn(`"id":"fco_11111111-2222-3333-4444-555555555555"`)
	if len(found) != 1 || !writtenByHand(found[0]) {
		t.Fatalf("uuidsIn found %q; want the one placeholder, written by hand", found)
	}
	if writtenByHand("7c3e9a52-4b1d-4f86-a0e2-93d5c6b7f814") {
		t.Fatal("a generated id was taken for a hand-written one")
	}
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
