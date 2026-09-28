// Package hogs finds the managed sessions whose process trees are holding the
// machine's CPU or memory, decides how urgent that is, and words the notice the
// board queues for the session itself. It never signals a process: the session
// that started the work is the one that knows whether it is the deliverable.
package hogs

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
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
	PID     int
	Command string
	// CPUPercent is this process's share over the last interval, 100 being
	// one full core. It counts children the process reaped in that interval,
	// so a make or a go build carries the compilers it ran.
	CPUPercent float64
	// MemBytes is PSS where the kernel exposes it, RSS otherwise.
	MemBytes uint64
}

// Usage is one session's tree at one sample.
type Usage struct {
	// CPUPercent is the tree's CPU over the interval since the previous
	// sample, 100 being one core. CPUValid is false on the first sample of a
	// tree, which has nothing to difference against.
	CPUPercent float64
	CPUValid   bool
	// MemBytes is the tree's resident memory: PSS summed where available, so
	// pages the tree's processes share are not counted once per process.
	MemBytes uint64
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
// It opens its own files rather than sharing internal/sysstat's walk: that one
// runs every poll and keeps only tree totals, where a notice has to name the
// processes, and this one runs every few seconds, so the per-process reads it
// adds (PSS, argv for the top few) are paid at a tenth of the rate.
//
// Not safe for concurrent use; the watcher that owns one runs one sample at a
// time.
type Sampler struct {
	root     string
	pageSize uint64
	trees    map[string]treeMemo
}

// NewSampler reads the /proc mounted at root; empty means /proc.
func NewSampler(root string) *Sampler {
	if root == "" {
		root = "/proc"
	}
	return &Sampler{root: root, pageSize: uint64(os.Getpagesize()), trees: map[string]treeMemo{}}
}

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
		pids, ok := s.childrenWalk(root)
		if !ok {
			// Without per-task child lists (a kernel built without
			// CONFIG_PROC_CHILDREN) the tree comes from one scan of every
			// process's parent, taken once per sample for every session.
			if parents == nil {
				parents = s.parentTable()
			}
			pids = walkParents(root, parents)
		}
		reads := make(map[procKey]procRead, len(pids))
		for _, pid := range pids {
			if read, ok := s.readProc(pid); ok {
				reads[read.key] = read
			}
		}
		if len(reads) == 0 {
			delete(s.trees, id)
			continue
		}
		usage := s.usage(id, now, uptimeOK, reads)
		out.Sessions[id] = usage
		s.trees[id] = treeMemo{at: now, uptime: uptime, procs: reads}
	}
	for id := range s.trees {
		if _, live := roots[id]; !live {
			delete(s.trees, id)
		}
	}
	return out
}

// usage differences this tree against its previous sample.
func (s *Sampler) usage(id string, now time.Time, uptimeOK bool, reads map[procKey]procRead) Usage {
	usage := Usage{Procs: len(reads)}
	type scored struct {
		read procRead
		cpu  float64
	}
	all := make([]scored, 0, len(reads))
	for _, read := range reads {
		usage.MemBytes += read.mem
		all = append(all, scored{read: read})
	}
	prev, seen := s.trees[id]
	elapsed := now.Sub(prev.at).Seconds()
	if seen && elapsed > 0 {
		usage.CPUValid = true
		var total, reapedGain, vanished float64
		for i := range all {
			read := all[i].read
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
				// way, or this is a sampler that missed it. Its history is not
				// this interval's, so it counts from here.
				delta = 0
			}
			delta = max(0, delta)
			all[i].cpu = delta / elapsed * 100
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
			if _, still := reads[key]; !still {
				vanished += before.own + before.reaped
			}
		}
		total -= min(vanished, reapedGain)
		usage.CPUPercent = max(0, total) / elapsed * 100
	}

	byCPU := slices.Clone(all)
	slices.SortFunc(byCPU, func(a, b scored) int {
		if a.cpu != b.cpu {
			if a.cpu > b.cpu {
				return -1
			}
			return 1
		}
		return a.read.key.pid - b.read.key.pid
	})
	byMem := slices.Clone(all)
	slices.SortFunc(byMem, func(a, b scored) int {
		if a.read.mem != b.read.mem {
			if a.read.mem > b.read.mem {
				return -1
			}
			return 1
		}
		return a.read.key.pid - b.read.key.pid
	})
	commands := map[int]string{}
	named := func(sc scored) ProcUsage {
		pid := sc.read.key.pid
		cmd, ok := commands[pid]
		if !ok {
			cmd = s.command(pid)
			commands[pid] = cmd
		}
		return ProcUsage{PID: pid, Command: cmd, CPUPercent: sc.cpu, MemBytes: sc.read.mem}
	}
	if usage.CPUValid {
		for _, sc := range byCPU[:min(topProcs, len(byCPU))] {
			if sc.cpu <= 0 {
				break
			}
			usage.TopCPU = append(usage.TopCPU, named(sc))
		}
	}
	for _, sc := range byMem[:min(topProcs, len(byMem))] {
		usage.TopMem = append(usage.TopMem, named(sc))
	}
	return usage
}

// childrenWalk lists root and everything under it through the per-task child
// lists. It reports false when the kernel does not expose them, so the caller
// can fall back; a root that has exited returns an empty list and true.
func (s *Sampler) childrenWalk(root int) ([]int, bool) {
	rootTasks := filepath.Join(s.root, strconv.Itoa(root), "task")
	tasks, err := os.ReadDir(rootTasks)
	if err != nil {
		return nil, true
	}
	if len(tasks) > 0 {
		if _, err := os.Stat(filepath.Join(rootTasks, tasks[0].Name(), "children")); err != nil {
			return nil, false
		}
	}
	// Breadth-first with a seen set, so a pid that somehow lists itself
	// cannot loop the walk.
	seen := map[int]bool{}
	queue := []int{root}
	var out []int
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
		base := filepath.Join(s.root, strconv.Itoa(pid), "task")
		entries, err := os.ReadDir(base)
		if err != nil {
			// Exited between being listed and being read.
			continue
		}
		for _, task := range entries {
			raw, err := os.ReadFile(filepath.Join(base, task.Name(), "children"))
			if err != nil {
				continue
			}
			for _, field := range strings.Fields(string(raw)) {
				if child, err := strconv.Atoi(field); err == nil {
					queue = append(queue, child)
				}
			}
		}
	}
	return out, true
}

// parentTable maps every process to its children from each one's ppid.
func (s *Sampler) parentTable() map[int][]int {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	parents := map[int][]int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.root, entry.Name(), "stat"))
		if err != nil {
			continue
		}
		fields, _, ok := statFields(raw)
		if !ok {
			continue
		}
		ppid, err := strconv.Atoi(fields[ppidField-3])
		if err != nil {
			continue
		}
		parents[ppid] = append(parents[ppid], pid)
	}
	return parents
}

func walkParents(root int, parents map[int][]int) []int {
	seen := map[int]bool{}
	queue := []int{root}
	var out []int
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
		queue = append(queue, parents[pid]...)
	}
	return out
}

// proc(5) numbers the stat fields from 1; statFields returns them from field
// 3 (state) on, so field N is at index N-3.
const (
	ppidField      = 4
	utimeField     = 14
	stimeField     = 15
	cutimeField    = 16
	cstimeField    = 17
	starttimeField = 22
	rssField       = 24
)

// statFields splits a stat line after its comm, which is parenthesised and
// may itself hold spaces and parentheses, and returns the comm too.
func statFields(raw []byte) ([]string, string, bool) {
	open := bytes.IndexByte(raw, '(')
	end := bytes.LastIndexByte(raw, ')')
	if open < 0 || end < open || end+2 > len(raw) {
		return nil, "", false
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) <= rssField-3 {
		return nil, "", false
	}
	return fields, string(raw[open+1 : end]), true
}

// readProc reads one process's times and memory. It reports false for a
// process that exited underneath the walk.
func (s *Sampler) readProc(pid int) (procRead, bool) {
	dir := filepath.Join(s.root, strconv.Itoa(pid))
	raw, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return procRead{}, false
	}
	fields, _, ok := statFields(raw)
	if !ok {
		return procRead{}, false
	}
	num := func(field int) float64 {
		v, _ := strconv.ParseFloat(fields[field-3], 64)
		return v
	}
	start, err := strconv.ParseUint(fields[starttimeField-3], 10, 64)
	if err != nil {
		return procRead{}, false
	}
	read := procRead{
		key:    procKey{pid: pid, start: start},
		own:    (num(utimeField) + num(stimeField)) / clockTicks,
		reaped: (num(cutimeField) + num(cstimeField)) / clockTicks,
	}
	if pss, ok := s.pss(dir); ok {
		read.mem = pss
	} else if pages, err := strconv.ParseUint(fields[rssField-3], 10, 64); err == nil {
		read.mem = pages * s.pageSize
	}
	return read, true
}

// pss reads a process's proportional set size from smaps_rollup, which
// divides each shared page among the processes mapping it. That matters
// here because a tree is summed: a browser's renderers or a forked worker
// pool share most of their pages, and RSS counts them once per process.
func (s *Sampler) pss(dir string) (uint64, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "smaps_rollup"))
	if err != nil {
		return 0, false
	}
	return kibField(raw, "Pss:")
}

// kibField reads a "Name:   123 kB" line out of a /proc file, in bytes.
func kibField(raw []byte, name string) (uint64, bool) {
	for line := range bytes.SplitSeq(raw, []byte{'\n'}) {
		rest, found := bytes.CutPrefix(line, []byte(name))
		if !found {
			continue
		}
		fields := strings.Fields(string(rest))
		if len(fields) == 0 {
			return 0, false
		}
		kib, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, false
		}
		return kib * 1024, true
	}
	return 0, false
}

func (s *Sampler) host() Host {
	raw, err := os.ReadFile(filepath.Join(s.root, "meminfo"))
	if err != nil {
		return Host{}
	}
	total, ok1 := kibField(raw, "MemTotal:")
	avail, ok2 := kibField(raw, "MemAvailable:")
	return Host{MemTotal: total, MemAvailable: avail, OK: ok1 && ok2 && total > 0}
}

func (s *Sampler) uptime() (float64, bool) {
	raw, err := os.ReadFile(filepath.Join(s.root, "uptime"))
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, false
	}
	up, err := strconv.ParseFloat(fields[0], 64)
	return up, err == nil
}

// command is a process's command line as a notice quotes it, or its comm for
// a process with no argv.
func (s *Sampler) command(pid int) string {
	dir := filepath.Join(s.root, strconv.Itoa(pid))
	cmd := ""
	if raw, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		cmd = strings.Join(strings.Fields(strings.ReplaceAll(string(raw), "\x00", " ")), " ")
	}
	if cmd == "" {
		if raw, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
			if _, comm, ok := statFields(raw); ok {
				cmd = "[" + comm + "]"
			}
		}
	}
	if len([]rune(cmd)) > commandWidth {
		cmd = string([]rune(cmd)[:commandWidth-1]) + "…"
	}
	return cmd
}
