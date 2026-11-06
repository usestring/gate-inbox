package tmux

import (
	"os"
	"strings"

	"github.com/usestring/gate-inbox/internal/envname"
)

// A process that double-forks or calls setsid has left the window's tree
// before the window closes, so the snapshot Kill takes never lists it. The
// Claude Code sandbox is the common case: its outer bwrap and socat proxy
// outlive the agent under init, and one board had thirty such trees, the
// oldest 49 days old. The launch tags every process in a session with its
// GATE_INBOX_SESSION_ID, and that tag is what finds them again.
//
// The tag alone is not enough to kill on. On the same board it was carried
// by the shared ~/.codex app-server daemon, which the first codex launch
// starts and every later one attaches to; by the stop worker, which runs
// with its target's id while it ends that target; and, after a revive, by
// both the old life's processes and the new one's. So a tagged process is
// reaped only when all of these hold: it hangs off init, it started no
// earlier than the window being ended, it is not this process or one of its
// ancestors, and it is not a daemon other sessions share.

// escapedProc is one process as the escaped pass reads it.
type escapedProc struct {
	pid, ppid int
	// start is the start time in clock ticks since boot, comparable between
	// processes on one machine.
	start uint64
	tag   string
	args  string
}

// sharedProcessMarkers name processes that outlive the session that
// started them on purpose and that other sessions depend on. The stop
// worker is here as well, because it runs under the id it is ending.
var sharedProcessMarkers = []string{
	"app-server-daemon",
	"opencode serve",
	"_finish-stop",
}

func shared(args string) bool {
	for _, marker := range sharedProcessMarkers {
		if strings.Contains(args, marker) {
			return true
		}
	}
	return false
}

// escapedFrom returns the pids to reap: every process tagged id that hangs
// off init and started at or after since, plus their tagged descendants.
// spare holds pids that must never be touched, whatever they carry.
func escapedFrom(procs []escapedProc, id string, since uint64, spare map[int]bool) []int {
	if id == "" || since == 0 {
		return nil
	}
	children := map[int][]escapedProc{}
	for _, p := range procs {
		children[p.ppid] = append(children[p.ppid], p)
	}
	eligible := func(p escapedProc) bool {
		return p.tag == id && p.start >= since && !spare[p.pid] && !shared(p.args)
	}
	var out []int
	seen := map[int]bool{}
	var queue []escapedProc
	for _, p := range children[1] {
		if eligible(p) {
			queue = append(queue, p)
		}
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if seen[p.pid] {
			continue
		}
		seen[p.pid] = true
		out = append(out, p.pid)
		for _, child := range children[p.pid] {
			if eligible(child) {
				queue = append(queue, child)
			}
		}
	}
	return out
}

// spareSelf is this process and every ancestor of it: a kill run from a
// shell that is itself tagged with the target's id must not end that shell.
// The chain is read from the process table rather than the tagged set, so
// an untagged link in it cannot hide a tagged ancestor above.
func spareSelf() map[int]bool {
	spare := map[int]bool{}
	for pid := os.Getpid(); pid > 1 && !spare[pid]; {
		spare[pid] = true
		parent, ok := parentOf(pid)
		if !ok {
			break
		}
		pid = parent
	}
	return spare
}

// withEscaped adds to tree the processes tagged id that escaped it. tree
// is the window snapshot taken before the kill, so its oldest member is the
// pane's own process and marks when this launch began; anything tagged id
// that is older belongs to an earlier life of the row and is left alone.
// Reading the table before the kill also keeps out anything started after.
func withEscaped(id string, tree []treeProc) []treeProc {
	if len(tree) == 0 {
		return tree
	}
	procs := readEscapedProcs()
	if len(procs) == 0 {
		return tree
	}
	// The tree is read on its own: a pane's process need not carry the tag
	// for its escapees to.
	var since uint64
	listed := make(map[int]bool, len(tree))
	for _, p := range tree {
		listed[p.pid] = true
		if start, ok := startOf(p.pid); ok && (since == 0 || start < since) {
			since = start
		}
	}
	pids := escapedFrom(procs, id, since, spareSelf())
	if len(pids) == 0 {
		return tree
	}
	table, err := processTable()
	if err != nil {
		return tree
	}
	for _, pid := range pids {
		row, ok := table[pid]
		if !ok || listed[pid] {
			continue
		}
		tree = append(tree, treeProc{pid: pid, started: row.started})
	}
	return tree
}

// tagOf is the session id an environment block carries, if any.
func tagOf(environ []byte) string {
	prefix := envname.SessionID + "="
	for _, entry := range strings.Split(string(environ), "\x00") {
		if value, ok := strings.CutPrefix(entry, prefix); ok {
			return value
		}
	}
	return ""
}
