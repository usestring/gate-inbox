//go:build darwin

package hogs

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/sysstat"
)

// darwinSource reads session trees the way the board's own process line
// does on macOS (sysstat.nativeTrees): one kern.proc.all sysctl for the whole
// table, then proc_pid_rusage for each process under a root.
//
// One rusage call carries everything a /proc stat line does here: own and
// reaped-children CPU time, which lets a tree keep the time of a compiler
// that started and exited between two samples, and memory. Memory is the
// phys_footprint rather than RSS, because RSS on macOS counts the shared
// library cache, tens of MB, again in every process, so a tree of many small
// processes would read as several times its real size.
//
// Measured on an M4 Mac with 38 panes over ~170 processes: ~0.3ms of CPU per
// sweep, ~1ms for all ~700 processes on the machine as one tree. At the
// 10-second default that is about 0.003% of a core.
type darwinSource struct{}

func newNativeSource() nativeSource {
	if !sysstat.RusageSupported() {
		return nil
	}
	return darwinSource{}
}

func (darwinSource) sweep(roots map[string]int) (map[string][]procRead, float64, bool) {
	clock := float64(time.Now().UnixMicro()) / 1e6
	table, ok := sysstat.ReadProcTable()
	if !ok {
		return nil, 0, false
	}
	trees := make(map[string][]procRead, len(roots))
	for id, root := range roots {
		if root <= 0 || !table.Has(root) {
			continue
		}
		var reads []procRead
		table.Walk(root, func(pid int) {
			ru, ok := sysstat.ReadTaskRusage(pid)
			if !ok {
				// Exited since the table was read, or someone else's.
				return
			}
			reads = append(reads, procRead{
				key:    procKey{pid: pid, start: table.StartMicros[pid]},
				own:    ru.CPUSeconds,
				reaped: ru.ChildCPUSeconds,
				mem:    ru.Footprint,
			})
		})
		trees[id] = reads
	}
	return trees, clock, true
}

func (darwinSource) host() Host {
	total, available, ok := sysstat.HostMemory()
	return Host{MemTotal: total, MemAvailable: available, OK: ok}
}

// command quotes an absolute argv[0] by its base name: bundle binaries such
// as framework Python run with a path long enough to push every argument out
// of the quote, and the pid beside it still finds the binary.
func (darwinSource) command(pid int) string {
	argv := sysstat.Argv(pid)
	if len(argv) > 0 && filepath.IsAbs(argv[0]) {
		argv[0] = filepath.Base(argv[0])
	}
	return strings.Join(argv, " ")
}
