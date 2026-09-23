package sysstat

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// markedTree builds the shape the argv check exists for: a shell holding two
// sleepers, one of which carries a --settings flag on its command line and one
// of which carries nothing.
//
// The flag's path is passed as an argument rather than written into the script,
// so the root shell's own argv holds "--settings \"$1\"" and never the expanded
// mark. A fixture whose root carried it would pass this test's positive case
// for the wrong reason and could never fail its negative one, which is what
// TestArgvMarkIsNotOnTheFixtureRoot guards.
func markedTree(t *testing.T) (root int, carrier int, mark string) {
	t.Helper()
	settings := filepath.Join(t.TempDir(), "claude-settings.json")
	pidFile := filepath.Join(t.TempDir(), "carrier.pid")
	script := `sh -c 'sleep 60' "$0" --settings "$1" & echo $! > "$2"; sh -c 'sleep 60' "$0" & wait`
	cmd := fixtureCommand(script, settings, pidFile)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a process tree: %v", err)
	}
	registerFixtureCleanup(t, cmd)
	root = cmd.Process.Pid

	deadline := time.Now().Add(60 * time.Second)
	for {
		raw, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				if stat, ok := Trees([]int{root})[root]; ok && stat.Procs >= 3 {
					return root, pid, "--settings " + settings
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the marked tree never came up under pid %d", root)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func fastSample(root int, mark string) ProcStat {
	sampler := NewTreeSampler(mark)
	want := map[int]bool{root: true}
	sampler.Sample([]int{root}, want) // seeding pass: the ps scan, which looks at no argv
	return sampler.Sample([]int{root}, want)[root]
}

// The plain case, and the one every healthy board is in: something under the
// pane carries the flag, so the tree reports itself wired.
func TestArgvMarkFindsAFlagOnAChild(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root, _, mark := markedTree(t)
	got := fastSample(root, mark)
	if !got.ArgvMarkOK {
		t.Fatalf("a sampler built with a mark reported ArgvMarkOK false: %+v", got)
	}
	if !got.ArgvMark {
		t.Errorf("the tree carries %q on a child and the sampler did not find it", mark)
	}
}

// The incident. A pane where the process carrying the flag has been stopped by
// job control and a hand-started one is doing the work reads as unwired, even
// though the flag is still in the tree: a stopped process writes no hook file,
// so its command line describes what it would do if somebody resumed it.
func TestArgvMarkIgnoresAStoppedCarrier(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root, carrier, mark := markedTree(t)
	if err := syscall.Kill(carrier, syscall.SIGSTOP); err != nil {
		t.Skipf("cannot stop the carrier: %v", err)
	}
	// Stopping is not instantaneous: the state in /proc turns T when the
	// kernel next schedules it out.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if info, ok := readProcStat(carrier); ok && info.stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never reached the stopped state", carrier)
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := fastSample(root, mark)
	if !got.ArgvMarkOK {
		t.Fatalf("ArgvMarkOK false with a mark configured: %+v", got)
	}
	if got.ArgvMark {
		t.Errorf("the only process carrying %q is stopped, and the tree still reported itself marked", mark)
	}

	// And it comes back, which is what makes the board's mark lapse on a
	// relaunch instead of needing a sweep.
	if err := syscall.Kill(carrier, syscall.SIGCONT); err != nil {
		t.Fatalf("cannot continue the carrier: %v", err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		if fastSample(root, mark).ArgvMark {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the tree did not read as marked again after the carrier was continued")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The fixture has to be honest about where the mark is, or the test above
// cannot fail.
func TestArgvMarkIsNotOnTheFixtureRoot(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root, _, mark := markedTree(t)
	if argvHas(root, mark) {
		t.Fatalf("the fixture's own root carries %q, so the stopped-carrier test proves nothing", mark)
	}
}

// A sampler nobody gave a mark to must not report every tree as unmarked:
// that false is "we did not look", and a caller reading ArgvMark without
// ArgvMarkOK would take it as "the flag is gone" for the whole board.
func TestNoMarkMeansNotLooked(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root, _, _ := markedTree(t)
	got := fastSample(root, "")
	if got.ArgvMarkOK {
		t.Errorf("a sampler with no mark claimed to have looked: %+v", got)
	}
	if got.ArgvMark {
		t.Errorf("a sampler with no mark reported a mark found")
	}
}

// The seeding pass is the ps-based full scan, which reads no argv at all. It
// has to say so rather than report the board unmarked, because it is the pass
// that runs when the board has just started -- the moment an operator is most
// likely to be looking at it.
func TestTheSeedingPassDoesNotClaimToHaveLooked(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root, _, mark := markedTree(t)
	sampler := NewTreeSampler(mark)
	want := map[int]bool{root: true}
	first := sampler.Sample([]int{root}, want)[root]
	if !first.OK {
		t.Fatalf("the seeding scan did not resolve the tree: %+v", first)
	}
	if first.ArgvMarkOK {
		t.Errorf("the seeding ps scan claimed to have read argv: %+v", first)
	}
	if second := sampler.Sample([]int{root}, want)[root]; !second.ArgvMarkOK {
		t.Errorf("the pass after seeding did not read argv: %+v", second)
	}
}

// The narrowing must not cost a tree that was asked for. Over-narrowing here
// is worse than the cost it saves: the board's hookless mark would silently
// stop appearing, which is the failure the mark exists to catch.
func TestARequestedRootIsStillScanned(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root, _, mark := markedTree(t)
	sampler := NewTreeSampler(mark)
	want := map[int]bool{root: true}
	sampler.Sample([]int{root}, want)
	got := sampler.Sample([]int{root}, want)[root]
	if !got.ArgvMarkOK {
		t.Fatalf("a requested root was not scanned: %+v", got)
	}
	if !got.ArgvMark {
		t.Errorf("a requested root carrying %q reported no mark", mark)
	}
}

// And a root nobody asked about reports "not looked at" rather than "not
// found", so the caller cannot read the skip as a finding.
func TestAnUnrequestedRootIsNotScanned(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root, _, mark := markedTree(t)
	sampler := NewTreeSampler(mark)
	sampler.Sample([]int{root}, nil)
	got := sampler.Sample([]int{root}, nil)[root]
	if got.ArgvMarkOK {
		t.Errorf("an unrequested root claimed to have been scanned: %+v", got)
	}
	if got.ArgvMark {
		t.Errorf("an unrequested root reported a mark it was never asked to look for")
	}
	if got.Procs == 0 || !got.OK {
		t.Errorf("skipping argv must not cost the rest of the sample: %+v", got)
	}
}
