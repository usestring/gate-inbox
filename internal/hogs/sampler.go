// Package hogs finds the managed sessions whose process trees are holding the
// machine's CPU or memory, decides how urgent that is, and words the notice the
// board queues for the session itself. It never signals a process: the session
// that started the work is the one that knows whether it is the deliverable.
package hogs

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/sysstat"
)

// clockTicks is USER_HZ, the unit /proc/<pid>/stat reports CPU time and start
// time in. It is 100 on every Linux Gate Inbox runs on, the same assumption
// internal/sysstat makes.
const clockTicks = 100

// topProcs is how many processes a usage names, by CPU and by memory. A
// notice lists what to look at, not the whole tree.
const topProcs = 3

// commandWidth bounds how much of a command line a notice quotes.
const commandWidth = 120

// ProcUsage is one process in a session's tree as a notice names it.
type ProcUsage struct {
	PID int
	// Command is empty until Sampler.Name fills it: argv is read only for a
	// session that is about to be told something.
	Command string
	// CPUPercent is this process's share over the last interval, 100 being
	// one full core. It counts children the process reaped in that interval,
	// so a make or a go build carries the compilers it ran.
	CPUPercent float64
	// MemBytes is PSS for a tree read at PSS, RSS otherwise (see Usage.PSS).
	MemBytes uint64
}

// Usage is one session's tree at one sample.
type Usage struct {
	// CPUPercent is the tree's CPU over the interval since the previous
	// sample, 100 being one core. CPUValid is false on the first sample of a
	// tree, which has nothing to difference against.
	CPUPercent float64
	CPUValid   bool
	// MemBytes is the tree's resident memory. PSS reports whether it was
	// summed as PSS, which counts a page shared among the tree's processes
	// once rather than once per process; a tree whose RSS stays under the
	// sampler's PSS floor is left at RSS, because reading PSS is expensive
	// and a tree under the floor is under every size rule either way.
	MemBytes uint64
	PSS      bool
	Procs    int
	// TopCPU and TopMem are the heaviest processes by each measure, heaviest
	// first.
	TopCPU []ProcUsage
	TopMem []ProcUsage
}

// Host is the machine-wide memory reading a sample takes alongside the trees.
type Host struct {
	MemTotal     uint64
	MemAvailable uint64
	OK           bool
}

// AvailablePercent is MemAvailable as a share of MemTotal, 0-100.
func (h Host) AvailablePercent() float64 {
	if !h.OK || h.MemTotal == 0 {
		return 100
	}
	return float64(h.MemAvailable) / float64(h.MemTotal) * 100
}

// Sample is one pass over every session's tree.
type Sample struct {
	At       time.Time
	Host     Host
	Sessions map[string]Usage
}

// procKey names a process across samples. The start time is part of it so a
// recycled pid is a new process rather than a continuation of the old one.
type procKey struct {
	pid   int
	start uint64
}

// procRead is what one walk learned about one process.
type procRead struct {
	key procKey
	// own is the process's own user+system seconds; reaped is the seconds of
	// the children it has waited for. Their sum only ever grows while the
	// process lives, and a child reaped inside the tree moves its time from
	// its own row into its parent's, which is what lets the tree total survive
	// a short-lived compiler that starts and exits between two samples.
	own, reaped float64
	mem         uint64
}

// treeMemo is what the sampler keeps of one session's tree between samples.
type treeMemo struct {
	at     time.Time
	uptime float64
	procs  map[procKey]procRead
}

// Sampler reads session process trees out of a /proc filesystem.
//
// Its cost is dominated by what the kernel has to do to answer, not by what
// is parsed. A stat is ~10µs. A smaps_rollup walks the process's whole
// address space under its mmap lock: ~5ms for one Chrome renderer on the
// board this was measured on, 1.9s for every process on that machine against
// 22ms for their stats -- and the lock it takes stalls the target's own page
// faults while it is held. So memory is screened on RSS, which stat already
// carries, and PSS is read only for a tree whose RSS has reached pssFloor.
//
// Not safe for concurrent use; the watcher that owns one runs one sample at a
// time.
type Sampler struct {
	root     string
	pageSize uint64
	trees    map[string]treeMemo
	// pssFloor is the tree RSS at which memory is read as PSS. PSS is never
	// more than RSS, so a tree under the smallest size a rule tests cannot
	// cross it by being read more precisely.
	pssFloor uint64
	// childLists is whether the kernel exposes task/<tid>/children: 0 until
	// the first walk finds out, then 1 or -1. Without them (a kernel built
	// without CONFIG_PROC_CHILDREN) the tree comes from every process's ppid.
	childLists int
}

// NewSampler reads the /proc mounted at root; empty means /proc. It reads
// no PSS until SetPSSFloor says from what size.
func NewSampler(root string) *Sampler {
	if root == "" {
		root = "/proc"
	}
	return &Sampler{root: root, pageSize: uint64(os.Getpagesize()), trees: map[string]treeMemo{}, pssFloor: math.MaxUint64}
}

// SetPSSFloor sets the tree RSS from which memory is summed as PSS.
func (s *Sampler) SetPSSFloor(bytes uint64) { s.pssFloor = bytes }

// Supported reports whether root looks like a Linux /proc this sampler can
// read. Everywhere else hog detection is off.
func (s *Sampler) Supported() bool {
	_, err := os.Stat(filepath.Join(s.root, "uptime"))
	return err == nil
}

// Sample reads every session's tree. roots maps a session id to its pane's
// root pid. A session whose root has exited is left out of the result.
func (s *Sampler) Sample(now time.Time, roots map[string]int) Sample {
	out := Sample{At: now, Host: s.host(), Sessions: make(map[string]Usage, len(roots))}
	uptime, uptimeOK := s.uptime()
	var parents map[int][]int
	for id, root := range roots {
		if root <= 0 {
			continue
		}
		var reads []procRead
		var rss uint64
		if s.hasChildLists(root) {
			reads, rss = s.walk(root)
		} else {
			if parents == nil {
				parents = s.parentTable()
			}
			reads, rss = s.walkTable(root, parents)
		}
		if len(reads) == 0 {
			delete(s.trees, id)
			continue
		}
		pss := rss >= s.pssFloor
		if pss {
			for i := range reads {
				if v, ok := s.pss(reads[i].key.pid); ok {
					reads[i].mem = v
				}
			}
		}
		memo := make(map[procKey]procRead, len(reads))
		for _, r := range reads {
			memo[r.key] = r
		}
		usage := s.usage(id, now, uptimeOK, reads, memo)
		usage.PSS = pss
		out.Sessions[id] = usage
		s.trees[id] = treeMemo{at: now, uptime: uptime, procs: memo}
	}
	for id := range s.trees {
		if _, live := roots[id]; !live {
			delete(s.trees, id)
		}
	}
	return out
}

// usage differences this tree against its previous sample.
func (s *Sampler) usage(id string, now time.Time, uptimeOK bool, reads []procRead, memo map[procKey]procRead) Usage {
	usage := Usage{Procs: len(reads)}
	cpu := make([]float64, len(reads))
	for _, read := range reads {
		usage.MemBytes += read.mem
	}
	prev, seen := s.trees[id]
	elapsed := now.Sub(prev.at).Seconds()
	if seen && elapsed > 0 {
		usage.CPUValid = true
		var total, reapedGain, vanished float64
		for i, read := range reads {
			before, known := prev.procs[read.key]
			var delta float64
			switch {
			case known:
				delta = (read.own + read.reaped) - (before.own + before.reaped)
				reapedGain += max(0, read.reaped-before.reaped)
			case uptimeOK && float64(read.key.start)/clockTicks >= prev.uptime:
				// Started since the last sample: all of its time is new.
				delta = read.own + read.reaped
			default:
				// Present before but not seen -- it joined the tree some other
				// way. Its history is not this interval's, so it counts from
				// here.
				delta = 0
			}
			delta = max(0, delta)
			cpu[i] = delta / elapsed * 100
			total += delta
		}
		// A process that exited since the last sample and was reaped by a
		// parent in this tree reappears whole in that parent's reaped time,
		// including the part the last sample already counted. That part is
		// taken back out, but never more than the reaped time the tree
		// actually gained: a process that left by being reparented out of the
		// tree was not reaped here, and subtracting its lifetime would erase
		// real work.
		for key, before := range prev.procs {
			if _, still := memo[key]; !still {
				vanished += before.own + before.reaped
			}
		}
		total -= min(vanished, reapedGain)
		usage.CPUPercent = max(0, total) / elapsed * 100
	}

	var topCPU, topMem [topProcs]int
	var nCPU, nMem int
	insert := func(top *[topProcs]int, n *int, i int, better func(a, b int) bool) {
		at := *n
		for at > 0 && better(i, top[at-1]) {
			at--
		}
		if at >= topProcs {
			return
		}
		end := min(*n, topProcs-1)
		copy(top[at+1:end+1], top[at:end])
		top[at] = i
		*n = min(*n+1, topProcs)
	}
	for i := range reads {
		if usage.CPUValid && cpu[i] > 0 {
			insert(&topCPU, &nCPU, i, func(a, b int) bool {
				return cpu[a] > cpu[b] || cpu[a] == cpu[b] && reads[a].key.pid < reads[b].key.pid
			})
		}
		insert(&topMem, &nMem, i, func(a, b int) bool {
			return reads[a].mem > reads[b].mem || reads[a].mem == reads[b].mem && reads[a].key.pid < reads[b].key.pid
		})
	}
	for _, i := range topCPU[:nCPU] {
		usage.TopCPU = append(usage.TopCPU, ProcUsage{PID: reads[i].key.pid, CPUPercent: cpu[i], MemBytes: reads[i].mem})
	}
	for _, i := range topMem[:nMem] {
		usage.TopMem = append(usage.TopMem, ProcUsage{PID: reads[i].key.pid, CPUPercent: cpu[i], MemBytes: reads[i].mem})
	}
	return usage
}

// Name fills in the command line of every process usage names.
func (s *Sampler) Name(usage *Usage) {
	for _, list := range [][]ProcUsage{usage.TopCPU, usage.TopMem} {
		for i := range list {
			list[i].Command = s.command(list[i].PID)
		}
	}
}

func (s *Sampler) procPath(pid int, rest string) string {
	return s.root + "/" + strconv.Itoa(pid) + rest
}

// hasChildLists reports whether the kernel exposes per-task child lists,
// finding out on the first root that has a /proc entry.
func (s *Sampler) hasChildLists(root int) bool {
	if s.childLists == 0 {
		if _, err := os.Stat(s.procPath(root, "")); err == nil {
			s.childLists = -1
			if _, err := os.Stat(s.procPath(root, "/task/"+strconv.Itoa(root)+"/children")); err == nil {
				s.childLists = 1
			}
		}
	}
	return s.childLists >= 0
}

// walk reads root and everything under it through the per-task child lists,
// returning each process and the tree's RSS.
func (s *Sampler) walk(root int) ([]procRead, uint64) {
	var reads []procRead
	var rss uint64
	// Breadth-first with a seen set, so a pid that somehow lists itself
	// cannot loop the walk.
	seen := map[int]bool{}
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		st, ok := s.readStat(pid)
		if !ok {
			// Exited between being listed and being read.
			continue
		}
		reads = append(reads, st.read)
		rss += st.read.mem
		// A single-threaded process has exactly one task, named for itself,
		// so its child list is read without listing the directory first.
		if st.threads <= 1 {
			queue = s.appendChildren(queue, s.procPath(pid, "/task/"+strconv.Itoa(pid)+"/children"))
			continue
		}
		base := s.procPath(pid, "/task")
		sysstat.ListProcDir(base, func(task string) {
			queue = s.appendChildren(queue, base+"/"+task+"/children")
		})
	}
	return reads, rss
}

func (s *Sampler) appendChildren(queue []int, path string) []int {
	sysstat.ReadProcFile(path, func(raw []byte) {
		for len(raw) > 0 {
			raw = bytes.TrimLeft(raw, " \n")
			end := bytes.IndexAny(raw, " \n")
			if end < 0 {
				end = len(raw)
			}
			if child, ok := parseUint(raw[:end]); ok {
				queue = append(queue, int(child))
			}
			raw = raw[end:]
		}
	})
	return queue
}

// parentTable maps every process to its children from each one's ppid.
func (s *Sampler) parentTable() map[int][]int {
	parents := map[int][]int{}
	sysstat.ListProcDir(s.root, func(name string) {
		pid, err := strconv.Atoi(name)
		if err != nil {
			return
		}
		if st, ok := s.readStat(pid); ok {
			parents[st.ppid] = append(parents[st.ppid], pid)
		}
	})
	return parents
}

func (s *Sampler) walkTable(root int, parents map[int][]int) ([]procRead, uint64) {
	var reads []procRead
	var rss uint64
	seen := map[int]bool{}
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if st, ok := s.readStat(pid); ok {
			reads = append(reads, st.read)
			rss += st.read.mem
		}
		queue = append(queue, parents[pid]...)
	}
	return reads, rss
}

type statRead struct {
	read    procRead
	ppid    int
	threads int
}

// proc(5) numbers the stat fields from 1; the scan below counts from field 3
// (state), the first after the comm.
const (
	ppidField      = 4
	utimeField     = 14
	stimeField     = 15
	cutimeField    = 16
	cstimeField    = 17
	threadsField   = 20
	starttimeField = 22
	rssField       = 24
)

// readStat reads one process's stat line. It reports false for a process
// that exited underneath the walk.
func (s *Sampler) readStat(pid int) (statRead, bool) {
	var st statRead
	ok := false
	sysstat.ReadProcFile(s.procPath(pid, "/stat"), func(raw []byte) {
		st, ok = parseStat(raw, s.pageSize)
	})
	st.read.key.pid = pid
	return st, ok
}

// parseStat pulls the fields the sampler uses out of a stat line without
// allocating. The fields are located after the last ')' because a comm is
// parenthesised and may itself contain spaces and parentheses.
func parseStat(raw []byte, pageSize uint64) (statRead, bool) {
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 {
		return statRead{}, false
	}
	rest := raw[end+1:]
	var st statRead
	var utime, stime, cutime, cstime uint64
	field := 2
	found := 0
	for len(rest) > 0 && field < rssField {
		rest = bytes.TrimLeft(rest, " ")
		stop := bytes.IndexByte(rest, ' ')
		if stop < 0 {
			stop = len(rest)
		}
		token := rest[:stop]
		rest = rest[stop:]
		field++
		var dst *uint64
		var v uint64
		switch field {
		case ppidField, threadsField:
			dst = &v
		case utimeField:
			dst = &utime
		case stimeField:
			dst = &stime
		case cutimeField:
			dst = &cutime
		case cstimeField:
			dst = &cstime
		case starttimeField:
			dst = &st.read.key.start
		case rssField:
			dst = &v
		default:
			continue
		}
		n, ok := parseInt(token)
		if !ok {
			return statRead{}, false
		}
		*dst = n
		found++
		switch field {
		case ppidField:
			st.ppid = int(v)
		case threadsField:
			st.threads = int(v)
		case rssField:
			st.read.mem = v * pageSize
		}
	}
	if found != 8 {
		return statRead{}, false
	}
	st.read.own = float64(utime+stime) / clockTicks
	st.read.reaped = float64(cutime+cstime) / clockTicks
	return st, true
}

// parseInt reads a decimal stat field, clamping a negative one (cutime can
// be, briefly, on some kernels) to zero.
func parseInt(b []byte) (uint64, bool) {
	if len(b) > 0 && b[0] == '-' {
		_, ok := parseUint(b[1:])
		return 0, ok
	}
	return parseUint(b)
}

func parseUint(b []byte) (uint64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	var n uint64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	return n, true
}

// pss reads a process's proportional set size from smaps_rollup, which
// divides each shared page among the processes mapping it. That matters
// here because a tree is summed: a browser's renderers or a forked worker
// pool share most of their pages, and RSS counts them once per process.
func (s *Sampler) pss(pid int) (uint64, bool) {
	var v uint64
	ok := false
	sysstat.ReadProcFile(s.procPath(pid, "/smaps_rollup"), func(raw []byte) {
		v, ok = kibField(raw, "Pss:")
	})
	return v, ok
}

// kibField reads a "Name:   123 kB" line out of a /proc file, in bytes.
func kibField(raw []byte, name string) (uint64, bool) {
	for line := range bytes.SplitSeq(raw, []byte{'\n'}) {
		rest, found := bytes.CutPrefix(line, []byte(name))
		if !found {
			continue
		}
		rest = bytes.TrimSpace(rest)
		if end := bytes.IndexByte(rest, ' '); end >= 0 {
			rest = rest[:end]
		}
		kib, ok := parseUint(rest)
		return kib * 1024, ok
	}
	return 0, false
}

func (s *Sampler) host() Host {
	var h Host
	sysstat.ReadProcFile(s.root+"/meminfo", func(raw []byte) {
		total, ok1 := kibField(raw, "MemTotal:")
		avail, ok2 := kibField(raw, "MemAvailable:")
		h = Host{MemTotal: total, MemAvailable: avail, OK: ok1 && ok2 && total > 0}
	})
	return h
}

func (s *Sampler) uptime() (float64, bool) {
	var up float64
	ok := false
	sysstat.ReadProcFile(s.root+"/uptime", func(raw []byte) {
		if end := bytes.IndexByte(raw, ' '); end > 0 {
			v, err := strconv.ParseFloat(string(raw[:end]), 64)
			up, ok = v, err == nil
		}
	})
	return up, ok
}

// command is a process's command line as a notice quotes it, or its comm for
// a process with no argv.
func (s *Sampler) command(pid int) string {
	cmd := ""
	sysstat.ReadProcFile(s.procPath(pid, "/cmdline"), func(raw []byte) {
		cmd = strings.Join(strings.Fields(strings.ReplaceAll(string(raw), "\x00", " ")), " ")
	})
	if cmd == "" {
		sysstat.ReadProcFile(s.procPath(pid, "/stat"), func(raw []byte) {
			open := bytes.IndexByte(raw, '(')
			end := bytes.LastIndexByte(raw, ')')
			if open >= 0 && end > open {
				cmd = "[" + string(raw[open+1:end]) + "]"
			}
		})
	}
	if len([]rune(cmd)) > commandWidth {
		cmd = string([]rune(cmd)[:commandWidth-1]) + "…"
	}
	return cmd
}
