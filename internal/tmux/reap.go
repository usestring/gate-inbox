package tmux

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Closing a pane sends SIGHUP to its foreground process group and nothing
// more. An agent's tool calls often run in a session of their own, so a dev
// server, a test watcher or the agent itself when it ignores the hangup
// outlives the window and is reparented to init, where nothing on the board
// can see it again. Kill therefore lists the window's process tree before it
// closes the window, and afterwards ends whatever on that list is still
// running: SIGTERM at once, SIGKILL after reapGrace.

const reapGrace = 3 * time.Second

// treeProc is one process from the snapshot. started tells a survivor from a
// new process that was given the same pid after the old one exited.
type treeProc struct {
	pid     int
	started string
}

var reaps sync.WaitGroup

// AwaitReaps waits for the SIGKILL passes Kill left running, so a short-lived
// command such as the kill CLI does not exit before them.
func AwaitReaps() {
	within := reapGrace + 2*time.Second
	done := make(chan struct{})
	go func() {
		reaps.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(within):
	}
}

// windowTree is every process under the panes of target, roots included.
func (d *Driver) windowTree(target string) []treeProc {
	out, err := d.run("list-panes", "-t", target, "-F", "#{pane_pid}")
	if err != nil {
		return nil
	}
	var roots []int
	for _, line := range strings.Fields(out) {
		if pid, err := strconv.Atoi(line); err == nil && pid > 1 {
			roots = append(roots, pid)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	table, err := processTable()
	if err != nil {
		return nil
	}
	return descendants(table, roots)
}

type procRow struct {
	ppid    int
	started string
}

func processTable() (map[int]procRow, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,lstart=").Output()
	if err != nil {
		return nil, err
	}
	return parseProcessTable(string(out)), nil
}

func parseProcessTable(out string) map[int]procRow {
	table := map[int]procRow{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		if pidErr == nil && ppidErr == nil {
			table[pid] = procRow{ppid: ppid, started: strings.Join(fields[2:], " ")}
		}
	}
	return table
}

// descendants walks the table from roots breadth first. A root missing from
// the table has already exited and is left out.
func descendants(table map[int]procRow, roots []int) []treeProc {
	children := map[int][]int{}
	for pid, row := range table {
		children[row.ppid] = append(children[row.ppid], pid)
	}
	seen := map[int]bool{}
	var out []treeProc
	queue := append([]int(nil), roots...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		row, ok := table[pid]
		if seen[pid] || !ok || pid <= 1 {
			continue
		}
		seen[pid] = true
		out = append(out, treeProc{pid: pid, started: row.started})
		queue = append(queue, children[pid]...)
	}
	return out
}

// reapTree ends what is left of procs. SIGTERM goes out before it returns;
// SIGKILL follows reapGrace later in the background.
func reapTree(procs []treeProc) {
	if len(procs) == 0 {
		return
	}
	living := survivors(procs)
	for _, p := range living {
		_ = syscall.Kill(p.pid, syscall.SIGTERM)
	}
	if len(living) == 0 {
		return
	}
	reaps.Add(1)
	go func() {
		defer reaps.Done()
		deadline := time.Now().Add(reapGrace)
		for time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			if living = survivors(living); len(living) == 0 {
				return
			}
		}
		for _, p := range survivors(living) {
			_ = syscall.Kill(p.pid, syscall.SIGKILL)
		}
	}()
}

// survivors is the procs still running as the same process.
func survivors(procs []treeProc) []treeProc {
	table, err := processTable()
	if err != nil {
		return nil
	}
	var out []treeProc
	for _, p := range procs {
		if row, ok := table[p.pid]; ok && row.started == p.started {
			out = append(out, p)
		}
	}
	return out
}
