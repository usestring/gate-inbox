package adopt

import (
	"sort"

	"github.com/shirou/gopsutil/v4/process"
)

// treeDepth bounds the walk below a pane. An agent launched from a shell is one
// or two levels down; anything deeper is the agent's own subprocesses, which
// name themselves after whatever they are running rather than after the agent.
const treeDepth = 3

// ProcTable is one pass's view of who parented whom.
//
// It exists because the obvious way to ask -- gopsutil's Children, once per
// process -- reads /proc/<pid>/stat for every process on the box on every
// call. A scan of thirty panes spent 2.6 of its 2.75 seconds there. One
// snapshot and a map lookup answer the same questions for a fortieth of that.
type ProcTable struct {
	children map[int32][]int32
}

// NewProcTable snapshots the process table. Build one per scan and pass it
// around: a pane that forks after this is read as it was, which is what a scan
// wants anyway, since the panes it is comparing against were captured at the
// same moment.
func NewProcTable() *ProcTable {
	table := &ProcTable{children: map[int32][]int32{}}
	// A process table this user cannot read at all leaves an empty table
	// rather than no table: a walk then sees the pane's own process and
	// nothing below it, which is a weaker answer but still an answer.
	procs, err := process.Processes()
	if err != nil {
		return table
	}
	for _, proc := range procs {
		ppid, err := proc.Ppid()
		if err != nil {
			continue
		}
		table.children[ppid] = append(table.children[ppid], proc.Pid)
	}
	for _, children := range table.children {
		sort.Slice(children, func(i, j int) bool { return children[i] < children[j] })
	}
	return table
}

// Cmdlines returns the command lines of pid and its descendants. A process that
// has gone since the snapshot, or that this user cannot read, simply
// contributes nothing.
//
// A nil table reads the process table for itself, so a one-off caller with no
// scan around it still gets a real answer rather than a quietly empty one.
func (t *ProcTable) Cmdlines(pid int32) []string {
	var lines []string
	t.walk(pid, func(p int32) {
		proc, err := process.NewProcess(p)
		if err != nil {
			return
		}
		if line, err := proc.Cmdline(); err == nil && line != "" {
			lines = append(lines, line)
		}
	})
	return lines
}

// PIDs is pid and its descendants, to the same depth Cmdlines walks. An agent
// that records its own pid can be recognised by finding that pid in here,
// which is an identity rather than a resemblance.
//
// There is no one-shot spelling of this on purpose. The convenience wrapper
// that used to exist read the whole process table per call, and the naming
// sweep called it once per pane -- thirty full /proc scans every thirty
// seconds, on the box the agents are running on. Naming the table is what
// makes sharing one across a pass the obvious thing to do.
func (t *ProcTable) PIDs(pid int32) []int {
	var pids []int
	t.walk(pid, func(p int32) { pids = append(pids, int(p)) })
	return pids
}

func (t *ProcTable) walk(pid int32, visit func(int32)) {
	if pid <= 0 {
		return
	}
	if t == nil {
		t = NewProcTable()
	}
	var descend func(int32, int)
	descend = func(p int32, depth int) {
		visit(p)
		if depth >= treeDepth {
			return
		}
		for _, child := range t.children[p] {
			descend(child, depth+1)
		}
	}
	descend(pid, 0)
}
