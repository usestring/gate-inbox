//go:build darwin

package sysstat

import (
	"bytes"
	"encoding/binary"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// ProcTable is the kernel's whole process table as one kern.proc.all sysctl
// returns it: who each process's children are, and when each one started.
// Reading it starts nothing and costs ~0.25ms for ~700 processes on an M4
// Mac, which is what lets callers walk pane trees without forking ps.
type ProcTable struct {
	Children map[int][]int
	// StartMicros is each pid's start time in microseconds since the Unix
	// epoch, which tells a recycled pid apart from the process it replaced.
	StartMicros map[int]uint64
}

// ReadProcTable reads the process table. It reports false when the sysctl
// fails or returns nothing.
func ReadProcTable() (ProcTable, bool) {
	table, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil || len(table) == 0 {
		return ProcTable{}, false
	}
	out := ProcTable{
		Children:    make(map[int][]int, len(table)),
		StartMicros: make(map[int]uint64, len(table)),
	}
	for i := range table {
		pid, ppid := int(table[i].Proc.P_pid), int(table[i].Eproc.Ppid)
		if pid == 0 {
			// kern.proc.all lists the kernel as pid 0 with itself as its
			// parent; it is never under a pane, and listing it as its own
			// child would make every walk from 0 a loop.
			continue
		}
		out.Children[ppid] = append(out.Children[ppid], pid)
		start := table[i].Proc.P_starttime
		out.StartMicros[pid] = uint64(start.Sec)*1_000_000 + uint64(start.Usec)
	}
	return out, true
}

// Walk visits root and every process under it, each once.
func (t ProcTable) Walk(root int, visit func(pid int)) {
	walkTree(root, t.Children, visit)
}

// Has reports whether pid was in the table.
func (t ProcTable) Has(pid int) bool {
	_, ok := t.StartMicros[pid]
	return ok
}

// TaskRusage is what proc_pid_rusage reports for one process.
type TaskRusage struct {
	// CPUSeconds is the process's own user+system time; ChildCPUSeconds is
	// that of the children it has reaped.
	CPUSeconds, ChildCPUSeconds float64
	// Resident is RSS. Footprint is the kernel's phys_footprint, what
	// Activity Monitor's Memory column shows: the process's dirty private
	// memory including what has been compressed or swapped, without the
	// shared library pages every process maps and RSS counts again in each.
	Resident, Footprint uint64
}

// rusageInfoV2 is struct rusage_info_v2 from <sys/resource.h>.
type rusageInfoV2 struct {
	UUID                [16]byte
	UserTime            uint64
	SystemTime          uint64
	PkgIdleWkups        uint64
	InterruptWkups      uint64
	Pageins             uint64
	WiredSize           uint64
	ResidentSize        uint64
	PhysFootprint       uint64
	ProcStartAbstime    uint64
	ProcExitAbstime     uint64
	ChildUserTime       uint64
	ChildSystemTime     uint64
	ChildPkgIdleWkups   uint64
	ChildInterruptWkups uint64
	ChildPageins        uint64
	ChildElapsedAbstime uint64
	DiskioBytesread     uint64
	DiskioByteswritten  uint64
}

const rusageInfoV2Flavor = 2

// libproc is loaded once. gopsutil dlopens libSystem and resolves its symbols
// on every call, which measured 3x the cost of the call itself.
var libproc = sync.OnceValues(func() (func(pid int32, flavor int32, buf unsafe.Pointer) int32, float64) {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, 0
	}
	var rusage func(pid int32, flavor int32, buf unsafe.Pointer) int32
	var timebase func(info unsafe.Pointer) int32
	purego.RegisterLibFunc(&rusage, lib, "proc_pid_rusage")
	purego.RegisterLibFunc(&timebase, lib, "mach_timebase_info")
	// rusage times are mach absolute time units: nanoseconds on Intel,
	// 125/3 ns on Apple silicon.
	var info struct{ Numer, Denom uint32 }
	if timebase(unsafe.Pointer(&info)) != 0 || info.Denom == 0 {
		return nil, 0
	}
	return rusage, float64(info.Numer) / float64(info.Denom) / 1e9
})

// ReadTaskRusage reads one process's CPU and memory. It reports false for a
// process that has exited, or that another user owns.
func ReadTaskRusage(pid int) (TaskRusage, bool) {
	rusage, secondsPerTick := libproc()
	if rusage == nil {
		return TaskRusage{}, false
	}
	var ri rusageInfoV2
	if rusage(int32(pid), rusageInfoV2Flavor, unsafe.Pointer(&ri)) != 0 {
		return TaskRusage{}, false
	}
	return TaskRusage{
		CPUSeconds:      float64(ri.UserTime+ri.SystemTime) * secondsPerTick,
		ChildCPUSeconds: float64(ri.ChildUserTime+ri.ChildSystemTime) * secondsPerTick,
		Resident:        ri.ResidentSize,
		Footprint:       ri.PhysFootprint,
	}, true
}

// RusageSupported reports whether proc_pid_rusage could be loaded.
func RusageSupported() bool {
	rusage, _ := libproc()
	return rusage != nil
}

// Argv is a process's argument vector from kern.procargs2: a 32-bit argc,
// the executable path, NUL padding, then argc NUL-terminated arguments. Nil
// when the process is gone or not readable.
func Argv(pid int) []string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil
	}
	return parseProcargs2(raw)
}

func parseProcargs2(raw []byte) []string {
	if len(raw) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	if argc == 0 {
		return nil
	}
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	argv := make([]string, 0, argc)
	for len(argv) < argc && len(rest) > 0 {
		end = bytes.IndexByte(rest, 0)
		if end < 0 {
			end = len(rest)
		}
		argv = append(argv, string(rest[:end]))
		rest = rest[min(end+1, len(rest)):]
	}
	return argv
}

// HostMemory is RAM and the share the kernel says it can still hand out
// (kern.memorystatus_level), the same reading the board's memory gauge uses.
func HostMemory() (total, available uint64, ok bool) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil || total == 0 {
		return 0, 0, false
	}
	level, err := unix.SysctlUint32("kern.memorystatus_level")
	if err != nil || level > 100 {
		return 0, 0, false
	}
	return total, total - memUsedFromLevel(total, level), true
}
