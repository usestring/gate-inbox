package sysstat

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// clockTicks is the kernel's USER_HZ, which /proc/<pid>/stat reports CPU time
// in. It is 100 on every Linux this runs on and Go has no portable way to ask;
// a wrong value would scale every CPU number by a constant, which is what
// TestSamplerAgreesWithTheFullScan compares against ps to catch.
const clockTicks = 100

// TreeSampler answers the same question as Trees -- CPU, RSS and process count
// under each pane's process tree -- without forking ps every pass.
//
// The poll pass runs every two seconds, and ps was 99.4% of its procs phase:
// 14ms of fork against 80µs of parsing on a 519-process table. Dropping
// columns buys nothing, because the cost is walking every process in /proc,
// not formatting the output. The only lever is reading fewer processes, and
// the pane trees are ~109 pids of the 519.
//
// The tree is discovered through /proc/<pid>/task/<tid>/children rather than
// by reconstructing it from a table of every ppid on the machine. That is what
// makes this exact rather than a cache: each pass sees the children that exist
// at that moment, so a newly spawned agent is never missed and liveness --
// which the caller decides from a tree of one -- is never answered from a
// stale shape.
type TreeSampler struct {
	mu sync.Mutex
	// pcpu is each root's raw ps %cpu from the seeding scan. It is only ever
	// the caller's pre-delta fallback, used on the first pass before two
	// CPU-second samples exist, and the first pass is the full scan.
	pcpu   map[int]float64
	seeded bool
	// argvMark is a substring the walk looks for in the argv of the live
	// processes under each root, reported back as ProcStat.ArgvMark. Empty
	// asks for nothing and costs nothing, which is what a caller that does
	// not care passes.
	//
	// It exists because argv is the only place some facts about a process
	// live. A managed claude session is wired to its hooks by a --settings
	// flag on its command line and by nothing else, so a session someone
	// restarted by hand inside the pane is indistinguishable from a healthy
	// one until something reads that argv.
	argvMark string
}

func NewTreeSampler(argvMark string) *TreeSampler {
	return &TreeSampler{argvMark: argvMark}
}

// procChildren reports whether this kernel exposes the child lists the walk
// needs. Resolved once: without it every pass would open a file, fail, and
// fall back, paying for the attempt forever. It is absent on any platform
// without /proc, and on a Linux built without CONFIG_PROC_CHILDREN.
var procChildren = sync.OnceValue(func() bool {
	self := os.Getpid()
	_, err := os.ReadFile("/proc/" + strconv.Itoa(self) + "/task/" + strconv.Itoa(self) + "/children")
	return err == nil
})

// Sample returns the same map Trees does.
//
// argvRoots names the roots worth reading argv under. It is a subset rather
// than a flag because the short-circuit only pays off where the mark is
// found: a tree that carries it stops at the first hit, while a tree that
// never carries it reads every process's cmdline, every pass, to learn
// nothing. Scanning every root put that cost on exactly the trees that can
// never benefit -- a codex pane, an empty pane -- measured at 2.21x per
// sample on a four-process tree.
//
// A root outside the set is sampled with no mark at all, so its ProcStat
// reports ArgvMarkOK false: "not looked at" rather than "not found". That
// distinction is what stops a caller reading silence as a finding.
//
// The first call is a full scan, which seeds the pcpu fallback the caller
// needs before it has two CPU-second samples to difference. Every call after
// it walks /proc directly.
func (s *TreeSampler) Sample(rootPIDs []int, argvRoots map[int]bool) map[int]ProcStat {
	if len(rootPIDs) == 0 {
		return map[int]ProcStat{}
	}
	if !procChildren() {
		return Trees(rootPIDs)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.seeded {
		stats := Trees(rootPIDs)
		s.pcpu = make(map[int]float64, len(stats))
		for root, stat := range stats {
			s.pcpu[root] = stat.PCPU
		}
		s.seeded = true
		return stats
	}

	stats := make(map[int]ProcStat, len(rootPIDs))
	for _, root := range rootPIDs {
		mark := ""
		if argvRoots[root] {
			mark = s.argvMark
		}
		stat, alive := sampleTree(root, mark)
		if !alive {
			// A root with no /proc entry has exited. Leaving it out is what
			// Trees does for a pid ps did not list, and the caller reads a
			// missing entry as "no sample", never as "no processes".
			continue
		}
		stat.PCPU = s.pcpu[root]
		stats[root] = stat
	}
	return stats
}

// sampleTree walks a pid and everything under it, reading each process's
// numbers straight from /proc. It reports false only when the root itself is
// gone.
//
// argvMark, when set, also asks whether any live process in the tree carries
// that substring in its argv. "Live" excludes a job-control-stopped process:
// a stopped process runs nothing, so a flag on its command line describes
// what it would do if resumed rather than anything happening now.
func sampleTree(root int, argvMark string) (ProcStat, bool) {
	info, ok := readProcStat(root)
	if !ok {
		return ProcStat{}, false
	}
	pageSize := uint64(os.Getpagesize())
	stat := ProcStat{OK: true, Procs: 1, CPUSeconds: info.cpuSeconds, RSS: info.rssPages * pageSize}
	stat.ArgvMarkOK = argvMark != ""
	stat.ArgvMark = stat.ArgvMarkOK && liveCarriesMark(root, info, argvMark)

	// Breadth-first with an explicit queue rather than recursion: a pid that
	// somehow appeared under itself would otherwise be a stack overflow in a
	// process the operator is watching, and seen makes the cycle finite.
	seen := map[int]bool{root: true}
	queue := childrenOf(root, info.threads)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		info, ok := readProcStat(pid)
		if !ok {
			// Exited between being listed and being read. Its parent is
			// still live, so this is one process ending, not a failure.
			continue
		}
		stat.Procs++
		stat.CPUSeconds += info.cpuSeconds
		stat.RSS += info.rssPages * pageSize
		// Short-circuited on purpose. The mark is normally on the pane's
		// first child, so a healthy tree pays one cmdline read; only a tree
		// that does not carry it anywhere pays one per process, and that is
		// the tree somebody needs to be told about.
		if stat.ArgvMarkOK && !stat.ArgvMark {
			stat.ArgvMark = liveCarriesMark(pid, info, argvMark)
		}
		queue = append(queue, childrenOf(pid, info.threads)...)
	}
	return stat, true
}

// liveCarriesMark reports whether a process is one whose argv can speak for
// the tree -- running rather than stopped -- and does carry the mark.
func liveCarriesMark(pid int, info procInfo, mark string) bool {
	if info.stopped {
		return false
	}
	return argvHas(pid, mark)
}

// argvHas reports whether a process's command line contains mark. The
// arguments are NUL-separated in /proc, and they are joined with spaces so a
// mark can span the boundary between a flag and its value: "--settings /path"
// is two argv entries and one thing worth matching.
//
// An unreadable cmdline is false rather than unknown. It means the process
// exited underneath the walk, or is a kernel thread, which has no argv at
// all; neither is a process carrying the mark.
func argvHas(pid int, mark string) bool {
	found := false
	withProcFile("/proc/"+strconv.Itoa(pid)+"/cmdline", func(raw []byte) {
		argv := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		found = strings.Contains(strings.Join(argv, " "), mark)
	})
	return found
}

// childrenOf lists a process's direct children.
//
// Every thread is asked, not just the main one: children are recorded against
// the task that forked them, so a runtime that spawns from a worker thread --
// which is most of them -- would go unseen if this read only task/<pid>.
func childrenOf(pid int, threads int) []int {
	base := "/proc/" + strconv.Itoa(pid) + "/task"
	// A single-threaded process has exactly one task, named for the process
	// itself, so its child list can be read without listing the directory
	// first. Most of a board is shells, and the saved ReadDir is most of the
	// walk's cost.
	if threads <= 1 {
		return childPIDs(base + "/" + strconv.Itoa(pid) + "/children")
	}
	var out []int
	procDirNames(base, func(task string) {
		out = append(out, childPIDs(base+"/"+task+"/children")...)
	})
	return out
}

func childPIDs(path string) []int {
	var out []int
	withProcFile(path, func(raw []byte) {
		for _, field := range strings.Fields(string(raw)) {
			if child, err := strconv.Atoi(field); err == nil {
				out = append(out, child)
			}
		}
	})
	return out
}

// procInfo is what one /proc/<pid>/stat line is read for.
//
// A struct rather than a widening tuple: the fourth return was already one
// too many to read at a call site, and stopped -- which looks exactly like
// threads to a positional reader -- is the field a caller is most likely to
// take from the wrong slot.
type procInfo struct {
	cpuSeconds float64
	rssPages   uint64
	threads    int
	// stopped is job-control stopped ("T") or tracing stopped ("t"). Such a
	// process is scheduled nothing until somebody continues it.
	stopped bool
}

// readProcStat pulls cumulative CPU seconds, resident pages, thread count and
// run state out of one process.
func readProcStat(pid int) (procInfo, bool) {
	var info procInfo
	var ok bool
	read := withProcFile("/proc/"+strconv.Itoa(pid)+"/stat", func(raw []byte) {
		info, ok = parseProcStat(string(raw))
	})
	if !read {
		return procInfo{}, false
	}
	return info, ok
}

// parseProcStat reads the numbers out of one /proc/<pid>/stat line. It is
// split from the read so the os.ReadFile reference walk the tests compare
// against shares this parse: a comparison that re-implemented it too would be
// comparing two readers and two parsers, and could agree for the wrong reason.
//
// The fields are located after the last ')' because a process's comm is
// parenthesised and may itself contain spaces and parentheses -- "(sh -c (x))"
// is a legal comm, and splitting the line from the left walks off the end of
// it into the wrong columns.
func parseProcStat(line string) (procInfo, bool) {
	comm := strings.LastIndexByte(line, ')')
	if comm < 0 || comm+2 >= len(line) {
		return procInfo{}, false
	}
	// fields[0] is "state", which proc(5) numbers as field 3, so field N
	// lives at fields[N-3].
	fields := strings.Fields(line[comm+2:])
	const state, utime, stime, numThreads, rss = 3, 14, 15, 20, 24
	if len(fields) <= rss-3 {
		return procInfo{}, false
	}
	user, err1 := strconv.ParseFloat(fields[utime-3], 64)
	sys, err2 := strconv.ParseFloat(fields[stime-3], 64)
	count, err4 := strconv.Atoi(fields[numThreads-3])
	pages, err3 := strconv.ParseUint(fields[rss-3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return procInfo{}, false
	}
	return procInfo{
		cpuSeconds: (user + sys) / clockTicks,
		rssPages:   pages,
		threads:    count,
		stopped:    fields[state-3] == "T" || fields[state-3] == "t",
	}, true
}
