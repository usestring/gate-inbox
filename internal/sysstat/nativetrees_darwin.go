//go:build darwin

package sysstat

import (
	"bytes"
	"encoding/binary"

	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/unix"
)

// nativeTrees answers what Trees does on macOS without forking ps.
//
// macOS has no /proc, so the Linux sampler's walk never runs here and every
// poll pass forked ps twice: once for every process on the machine, once
// more for the pane children's names. On a loaded Mac that fork was most
// of a slow pass, and it grows with the load the board is meant to show.
//
// The process table comes from one kern.proc.all sysctl, which returns
// every pid and its parent without starting anything. CPU time and resident
// size are then read with proc_pidinfo, and only for the processes under
// the roots asked about. Argv comes from kern.procargs2, for the roots'
// direct children only, as nameChildren limits it.
//
// PCPU is left zero for the caller to fill in. ps's %cpu on macOS is a
// decaying average of recent use that no syscall here returns, and a
// lifetime average in its place would show an agent that has gone idle as
// still busy.
//
// It reports false when the table cannot be read, and the caller falls
// back to ps.
func nativeTrees(rootPIDs []int) (map[int]ProcStat, bool) {
	table, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil || len(table) == 0 {
		return nil, false
	}
	children := make(map[int][]int, len(table))
	listed := make(map[int]bool, len(table))
	for i := range table {
		pid, ppid := int(table[i].Proc.P_pid), int(table[i].Eproc.Ppid)
		children[ppid] = append(children[ppid], pid)
		listed[pid] = true
	}
	stats := make(map[int]ProcStat, len(rootPIDs))
	for _, root := range rootPIDs {
		if !listed[root] {
			continue
		}
		stat := ProcStat{OK: true}
		walkTree(root, children, func(pid int) {
			if pid == 0 {
				// kern.proc.all lists the kernel as pid 0 with itself as
				// its parent; it is never under a pane.
				return
			}
			cpuSeconds, rss := taskUsage(pid)
			stat.Procs++
			stat.CPUSeconds += cpuSeconds
			stat.RSS += rss
		})
		for _, child := range children[root] {
			if program := darwinArgv0(child); program != "" {
				stat.Children = append(stat.Children, program)
			}
		}
		stats[root] = stat
	}
	return stats, true
}

// taskUsage is one process's cumulative CPU seconds and resident bytes, as
// ps reports them in its time and rss columns. A process that exited since
// the table was read, or that belongs to another user, reads as zero, which
// is what a vanished pid already contributes to ps's sums.
func taskUsage(pid int) (float64, uint64) {
	proc := &process.Process{Pid: int32(pid)}
	var cpuSeconds float64
	if times, err := proc.Times(); err == nil {
		cpuSeconds = times.User + times.System
	}
	var rss uint64
	if mem, err := proc.MemoryInfo(); err == nil {
		rss = mem.RSS
	}
	return cpuSeconds, rss
}

// darwinArgv0 is a process's argv[0], the program as it was invoked, from
// kern.procargs2: a 32-bit argc, the executable path, NUL padding, then
// argv. Empty when the process is gone or not readable.
func darwinArgv0(pid int) string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return ""
	}
	if binary.LittleEndian.Uint32(raw[:4]) == 0 {
		return ""
	}
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return ""
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	if end = bytes.IndexByte(rest, 0); end >= 0 {
		rest = rest[:end]
	}
	return string(rest)
}
