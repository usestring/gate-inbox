package sysstat

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// livePaneRoots is the pane pids of the operator's real board, which is what
// the poll pass hands Trees. Read-only: one list-panes.
func livePaneRoots(tb testing.TB) []int {
	tb.Helper()
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid}").Output()
	if err != nil {
		tb.Skipf("no live board: %v", err)
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		tb.Skip("no panes on the live board")
	}
	return pids
}

// TestRealBoardProcsPhase breaks down the poll pass's "procs" phase against a
// real process table. The phase is three things, and only one of them is the
// process walk everyone assumes it is.
func TestRealBoardProcsPhase(t *testing.T) {
	if os.Getenv("PROCS_MEASURE") == "" {
		t.Skip("set PROCS_MEASURE=1 to measure the live board")
	}
	roots := livePaneRoots(t)
	total, _ := exec.Command("sh", "-c", "ps -axo pid= | wc -l").Output()
	sampler := NewTreeSampler("")
	sampler.Sample(roots, nil) // the seeding scan, so "shipped" below is the steady state

	for pass := 1; pass <= 5; pass++ {
		mark := time.Now()
		trees := sampler.Sample(roots, nil)
		treeTime := time.Since(mark)

		mark = time.Now()
		Trees(roots)
		baselineTime := time.Since(mark)

		mark = time.Now()
		ncpu := LogicalCPUs()
		cpuTime := time.Since(mark)

		mark = time.Now()
		memTotal, _ := MemTotalBytes()
		memTime := time.Since(mark)

		phase := treeTime + cpuTime + memTime
		// "shipped" is what the poll pass actually calls (TreeSampler.Sample);
		// "baseline" is the ps scan it replaced. Reporting only the latter is
		// how an earlier version of this probe was read as the shipped cost.
		fmt.Printf("pass %d  procs[shipped]=%-9s sampler=%-9s baseline[ps Trees]=%-9s cpucount=%-9s memtotal=%-9s roots=%d resolved=%d table=%s\n",
			pass,
			phase.Round(time.Microsecond),
			treeTime.Round(time.Microsecond),
			baselineTime.Round(time.Microsecond),
			cpuTime.Round(time.Microsecond),
			memTime.Round(time.Microsecond),
			len(roots), len(trees),
			strings.TrimSpace(string(total)))
		_ = ncpu
		_ = memTotal
	}
}

// TestRealBoardProcsAlternatives weighs the ways the procs phase could get
// cheaper, against the real table. The question is whether reading /proc for
// only the pids already known to be in pane trees beats one ps of everything.
func TestRealBoardProcsAlternatives(t *testing.T) {
	if os.Getenv("PROCS_MEASURE") == "" {
		t.Skip("set PROCS_MEASURE=1 to measure the live board")
	}
	roots := livePaneRoots(t)

	treePIDs := collectTree(roots)

	for pass := 1; pass <= 5; pass++ {
		mark := time.Now()
		raw, _ := exec.Command("ps", "-axo", "pid=,ppid=,pcpu=,rss=,time=").Output()
		forkTime := time.Since(mark)

		mark = time.Now()
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		parsed := 0
		for _, line := range lines {
			if len(strings.Fields(line)) == 5 {
				parsed++
			}
		}
		parseTime := time.Since(mark)

		// The tree-scoped alternative: one /proc/<pid>/stat read each.
		//
		// The pid list is rebuilt every pass. Reusing one snapshot made this
		// report a steady handful of "missed" pids that were simply processes
		// that had exited since it was taken -- most reliably the ps this
		// probe forks itself, which is a child of the pane the test runs in.
		// A dead pid is not a gap, so the two are counted apart: only
		// "unreadable" would mean a live process /proc could not answer for.
		treePIDs = collectTree(roots)
		mark = time.Now()
		read, died, unreadable := 0, 0, 0
		for _, pid := range treePIDs {
			b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
			if err == nil {
				read += len(b)
				continue
			}
			if _, alive := os.Stat("/proc/" + strconv.Itoa(pid)); alive != nil {
				died++
				continue
			}
			unreadable++
		}
		procfsTime := time.Since(mark)

		fmt.Printf("pass %d  ps.fork=%-9s ps.parse=%-9s (rows=%d)  procfs[%d pids]=%-9s exited=%d unreadable=%d\n",
			pass,
			forkTime.Round(time.Microsecond),
			parseTime.Round(time.Microsecond), parsed,
			len(treePIDs), procfsTime.Round(time.Microsecond), died, unreadable)
	}
}

// TestRealBoardSamplerCost is the before/after: one ps every pass against a
// /proc walk of the pane trees only.
//
// procs-agree is 28 of 29 rather than 29, every pass, and that is the harness
// observing itself: Trees forks ps, and that ps is a child of the pane this
// test runs in, so the full scan counts one process in that tree which the
// sampler -- which forks nothing -- correctly does not.
func TestRealBoardSamplerCost(t *testing.T) {
	if os.Getenv("PROCS_MEASURE") == "" {
		t.Skip("set PROCS_MEASURE=1 to measure the live board")
	}
	roots := livePaneRoots(t)
	sampler := NewTreeSampler("")
	for pass := 1; pass <= 8; pass++ {
		mark := time.Now()
		full := Trees(roots)
		fullTime := time.Since(mark)

		mark = time.Now()
		got := sampler.Sample(roots, nil)
		sampleTime := time.Since(mark)

		// A tree whose counts differ is only interesting if the processes
		// the sampler did not report are still alive. They never are: the
		// difference is the ps that Trees forks, which is a child of the pane
		// this test runs in and has exited by the time anyone looks. Counting
		// raw disagreement reported that as a permanent 1-tree gap.
		stale, live := 0, 0
		for root, s := range got {
			f, ok := full[root]
			if !ok || f.Procs == s.Procs {
				continue
			}
			if _, alive := sampleTree(root, ""); alive && len(clientsStillAlive(root, s.Procs, f.Procs)) > 0 {
				live++
				continue
			}
			stale++
		}
		fmt.Printf("pass %d  Trees=%-9s Sample=%-9s speedup=%4.1fx  roots=%d resolved=%d trees-differing-by-exited=%d trees-missing-live-procs=%d\n",
			pass, fullTime.Round(time.Microsecond), sampleTime.Round(time.Microsecond),
			float64(fullTime)/float64(sampleTime), len(roots), len(got), stale, live)
	}
}

// collectTree is the pids under the pane roots according to a fresh ps, which
// is the set a tree-scoped reader would have to visit.
func collectTree(roots []int) []int {
	out, _ := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	children := map[int][]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		children[ppid] = append(children[ppid], pid)
	}
	var pids []int
	seen := map[int]bool{}
	var walk func(int)
	walk = func(pid int) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		pids = append(pids, pid)
		for _, c := range children[pid] {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	return pids
}

// clientsStillAlive re-walks a tree and reports the pids a fresh ps sees that
// the /proc walk does not and that are still alive. A non-empty answer is the
// only shape that would mean the sampler can under-report a live tree.
func clientsStillAlive(root, sampled, scanned int) []int {
	if sampled >= scanned {
		return nil
	}
	children, procs := procTable()
	if children == nil {
		return nil
	}
	fromPS := map[int]bool{}
	if _, alive := procs[root]; alive {
		walkTree(root, children, func(pid int) { fromPS[pid] = true })
	}
	fromWalk := map[int]bool{root: true}
	info, ok := readProcStat(root)
	if ok {
		queue := childrenOf(root, info.threads)
		for len(queue) > 0 {
			pid := queue[0]
			queue = queue[1:]
			if fromWalk[pid] {
				continue
			}
			fromWalk[pid] = true
			childInfo, ok := readProcStat(pid)
			if ok {
				queue = append(queue, childrenOf(pid, childInfo.threads)...)
			}
		}
	}
	var missing []int
	for pid := range fromPS {
		if fromWalk[pid] {
			continue
		}
		if _, alive := readProcStat(pid); alive {
			missing = append(missing, pid)
		}
	}
	return missing
}
