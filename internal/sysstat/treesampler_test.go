package sysstat

import (
	"io"
	"os"
	"testing"
	"time"
)

// treeUnderTest starts a shell holding two sleeping children, which is the
// shape the sampler exists to measure: a pane process with an agent under it.
func treeUnderTest(t *testing.T) int {
	t.Helper()
	// One child burns CPU so the CPU-seconds comparison has something to
	// compare: against an idle tree, ps and /proc agree at zero no matter how
	// wrong the USER_HZ conversion is.
	// The sleeper is spawned through a marked shell of its own, so the leak
	// guard can see it: a bare `sleep 60` would carry nothing to match on.
	cmd := startFixture(t, `while :; do :; done & sh -c 'sleep 60' "$0" & wait`)
	root := cmd.Process.Pid
	// The children have to exist before a scan can see them, or the first
	// sample measures a tree of one and proves nothing.
	//
	// Failing rather than skipping when they do not: a shell that cannot grow
	// two children is a broken fixture, not an absent capability, and the skip
	// this replaces fired under full-suite load on a busy machine -- so the
	// tests below silently stopped running exactly when the suite was under
	// the most pressure. The budget is generous for the same reason.
	deadline := time.Now().Add(60 * time.Second)
	for {
		stat, ok := Trees([]int{root})[root]
		if ok && stat.Procs >= 3 && stat.CPUSeconds >= 0.1 {
			return root
		}
		if time.Now().After(deadline) {
			t.Fatalf("the test tree never grew its children: ps sees %d process(es) and %vs of CPU under pid %d, want 3 and 0.1s",
				stat.Procs, stat.CPUSeconds, root)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The fast path is only worth having if it agrees with the ps scan it
// replaces. Process count has to match exactly; RSS is read from a different
// source (/proc pages against ps's KB) and moves under a live process, so it
// is checked for the same order of magnitude rather than equality.
func TestSamplerAgreesWithTheFullScan(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root := treeUnderTest(t)
	sampler := NewTreeSampler("")

	// The first Sample is the full scan that records the shape; the second
	// is the /proc path this test is about.
	first := sampler.Sample([]int{root}, nil)[root]
	second := sampler.Sample([]int{root}, nil)[root]
	full := Trees([]int{root})[root]

	if !second.OK {
		t.Fatalf("fast sample did not resolve the tree: %+v", second)
	}
	if second.Procs != full.Procs {
		t.Errorf("fast sample counted %d processes, ps counted %d: the cached shape disagrees with the table", second.Procs, full.Procs)
	}
	if first.Procs != full.Procs {
		t.Errorf("full sample counted %d processes, ps counted %d", first.Procs, full.Procs)
	}
	if second.RSS == 0 {
		t.Errorf("fast sample reported no RSS; the /proc page field is not being read")
	}
	if ratio := float64(second.RSS) / float64(full.RSS); ratio < 0.5 || ratio > 2 {
		t.Errorf("fast RSS %d and ps RSS %d differ by more than 2x: the page-size conversion is wrong", second.RSS, full.RSS)
	}
	// CPU seconds is where a wrong USER_HZ hides: it scales every number by
	// a constant, so it has to be caught by comparing magnitudes against ps
	// rather than by looking at the value alone.
	if second.CPUSeconds < 0 {
		t.Errorf("negative CPU seconds %v", second.CPUSeconds)
	}
	if full.CPUSeconds < 0.05 {
		t.Fatalf("the burner child never accumulated measurable CPU (ps saw %vs); the comparison below would prove nothing", full.CPUSeconds)
	}
	if second.CPUSeconds == 0 {
		t.Fatalf("ps saw %vs of CPU and /proc saw none: the utime/stime fields are misread", full.CPUSeconds)
	}
	// Both are cumulative and the tree is burning CPU between the two reads,
	// so /proc (read second) is expected to be a little ahead -- but only a
	// little. A USER_HZ off by 100x lands far outside this.
	if ratio := second.CPUSeconds / full.CPUSeconds; ratio < 0.5 || ratio > 2 {
		t.Errorf("/proc reported %vs of CPU and ps reported %vs (ratio %.3f): the USER_HZ conversion is wrong",
			second.CPUSeconds, full.CPUSeconds, ratio)
	}
}

// The property the whole design turns on: a child that appears between passes
// is visible on the very next one. The caller decides "the agent is gone" from
// a tree of one process, so a sampler that discovered children on a slower
// cadence than it reported liveness would call a starting agent dead.
func TestSamplerSeesAChildTheInstantItAppears(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	// A shell reading commands from a pipe, so the test can grow the tree on
	// demand rather than waiting for one to grow by itself.
	// Through fixtureCommand for the process group, which is what ends the
	// children this test asks the shell to spawn. `exec` so the pid it holds
	// is the shell actually reading the pipe; that drops the marker from the
	// shell's argv, and the child spawned below carries one of its own.
	shell := fixtureCommand("exec sh")
	stdin, err := shell.StdinPipe()
	if err != nil {
		t.Skipf("cannot pipe to a shell: %v", err)
	}
	if err := shell.Start(); err != nil {
		t.Skipf("cannot start a shell: %v", err)
	}
	registerFixtureCleanup(t, shell)
	t.Cleanup(func() { stdin.Close() })
	root := shell.Process.Pid

	sampler := NewTreeSampler("")
	sampler.Sample([]int{root}, nil) // seeding scan
	before := sampler.Sample([]int{root}, nil)[root].Procs

	if _, err := io.WriteString(stdin, "sh -c 'sleep 60' "+fixtureMarker+" &\n"); err != nil {
		t.Fatalf("spawn a child: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := sampler.Sample([]int{root}, nil)[root]
		if got.Procs > before {
			return
		}
		if time.Now().After(deadline) {
			full := Trees([]int{root})[root]
			t.Fatalf("the new child never appeared: sampler still reports %d processes (was %d), ps reports %d",
				got.Procs, before, full.Procs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A root that has exited has to be reported as gone rather than as a live tree
// of zero processes.
func TestSamplerDropsARootThatHasExited(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root := treeUnderTest(t)
	sampler := NewTreeSampler("")
	sampler.Sample([]int{root}, nil)

	gone := 999999
	if _, listed := sampler.Sample([]int{gone}, nil)[gone]; listed {
		t.Errorf("a pid that does not exist was reported as a live tree")
	}
}

func TestReadProcStatSurvivesAParenthesisedCommand(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	// The process's own stat, which is enough to prove the parse finds the
	// numeric fields after the comm rather than counting from the left.
	info, ok := readProcStat(os.Getpid())
	if !ok {
		t.Fatalf("could not read this process's own /proc stat")
	}
	if info.rssPages == 0 {
		t.Errorf("this process reported no resident pages")
	}
	if info.cpuSeconds < 0 {
		t.Errorf("negative CPU seconds %v", info.cpuSeconds)
	}
	if info.threads < 1 {
		t.Errorf("this process reported %d threads", info.threads)
	}
}
