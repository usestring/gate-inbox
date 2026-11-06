package agentsession

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

// claudeShellMark is in the argv of every shell Claude Code's Bash tool
// starts, sandboxed or not: the command runs after sourcing the snapshot of
// the user's shell. A finished turn has no foreground command left, so one
// still up under a resting agent was started in the background.
const claudeShellMark = "/shell-snapshots/snapshot-"

// ProcessTable lists every process as "pid ppid args" rows.
func ProcessTable() ([]byte, error) {
	return exec.Command("ps", "-A", "-o", "pid=,ppid=,args=").Output()
}

// ClaudeShellsUnder counts the Claude Bash tool shells anywhere under pid.
// A shell the sandbox wraps carries the mark on both its wrapper and the
// shell inside, so only the outermost of a marked chain counts.
func ClaudeShellsUnder(pid int, table func() ([]byte, error)) int {
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
