package ui

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"

	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/store"
)

// claudeShellMark is in the argv of every shell Claude Code's Bash tool
// starts, sandboxed or not: the command runs after sourcing the snapshot of
// the user's shell. A finished turn has no foreground command left, so one
// still up under a resting agent was started in the background.
const claudeShellMark = "/shell-snapshots/snapshot-"

// childBackgroundWork says why a finished child must not be ended yet: the
// background tasks its transcript has not heard end, and whether a shell its
// agent started is still running under the pane. Ending the agent ends both,
// and the work and the report it was waiting on with them -- how a child
// with two judge runs going was archived on 2026-10-02, ten minutes after
// its spawner read a finish that was only the turn's.
//
// It is the sweep's own check, asked again just before it ends anything,
// whatever the row's status says: a status read off the pane can be wrong
// for as long as the pane is drawn in a way the rules do not expect.
func childBackgroundWork(tracker *agentsession.Background, sess store.Session, panePID func(string) (int, error)) (pending []string, shells int) {
	pending = claudeBackgroundPending(tracker, sess)
	pid, err := panePID(sess.ID)
	if err != nil || pid <= 0 {
		return pending, 0
	}
	return pending, claudeShellsUnder(pid, processTable)
}

// processTable lists every process as "pid ppid args" rows.
func processTable() ([]byte, error) {
	return exec.Command("ps", "-A", "-o", "pid=,ppid=,args=").Output()
}

// claudeShellsUnder counts the Claude Bash tool shells anywhere under pid.
// A shell the sandbox wraps carries the mark on both its wrapper and the
// shell inside, so only the outermost of a marked chain counts.
func claudeShellsUnder(pid int, table func() ([]byte, error)) int {
	out, err := table()
	if err != nil {
		return 0
	}
	children := map[int][]int{}
	marked := map[int]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		child, err1 := strconv.Atoi(fields[0])
		parent, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[parent] = append(children[parent], child)
		if strings.Contains(strings.Join(fields[2:], " "), claudeShellMark) {
			marked[child] = true
		}
	}
	count := 0
	queue := []int{pid}
	seen := map[int]bool{pid: true}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, kid := range children[next] {
			if seen[kid] {
				continue
			}
			seen[kid] = true
			if marked[kid] {
				count++
				continue
			}
			queue = append(queue, kid)
		}
	}
	return count
}

// rowByID is the row for id among rows the sweep copied off the loop.
func rowByID(rows []store.Session, id string) (store.Session, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	return store.Session{}, false
}
