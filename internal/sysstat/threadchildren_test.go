package sysstat

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// forkFromWorkerThread is a process that forks its child from a thread other
// than its main one. Python because Go will not do it: the runtime's forkExec
// runs on whichever thread it likes and in practice picks the main task, so a
// Go helper cannot construct this case.
const forkFromWorkerThread = `
import threading, subprocess, time, os
def worker():
    p = subprocess.Popen(["sleep", "30"])
    print("%d %d %d" % (os.getpid(), threading.get_native_id(), p.pid), flush=True)
    time.sleep(30)
threading.Thread(target=worker).start()
time.sleep(30)
`

// A child is recorded against the task that forked it, not against the
// process's main thread, so childrenOf has to read every task's list. On the
// operator's board 5 of the 81 children in the pane trees are recorded this
// way -- opencode's among them -- and reading only task/<pid>/children would
// be about six times cheaper and would silently lose every one of them.
//
// The single-threaded shortcut in childrenOf is safe for the same reason this
// is not: a process with one task has only its own list to read.
func TestChildrenOfFindsAChildForkedByAWorkerThread(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is what forks from a worker thread here")
	}
	script := filepath.Join(t.TempDir(), "forker.py")
	if err := os.WriteFile(script, []byte(forkFromWorkerThread), 0o600); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	// Through a marked shell in its own process group: the helper forks a
	// `sleep 30` that a kill aimed at python alone would leave running, and
	// the leak guard counts by the marker.
	cmd := fixtureCommand(`"$1" "$2"`, python, script)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start the helper: %v", err)
	}
	registerFixtureCleanup(t, cmd)

	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		if scanner.Scan() {
			line <- scanner.Text()
		}
		close(line)
	}()
	var reported string
	select {
	case reported = <-line:
	case <-time.After(20 * time.Second):
		t.Skip("the helper never reported its child")
	}
	var proc, tid, child int
	if _, err := fmt.Sscan(reported, &proc, &tid, &child); err != nil {
		t.Skipf("unreadable helper output %q: %v", reported, err)
	}

	// Without this the test would pass against a main-thread-only reader
	// whenever the runtime happened to fork from the main task, which is
	// exactly the case it exists to rule out.
	if tid == proc {
		t.Skip("the helper forked from its main thread, so this run proves nothing")
	}
	for _, pid := range childPIDs("/proc/" + strconv.Itoa(proc) + "/task/" + strconv.Itoa(proc) + "/children") {
		if pid == child {
			t.Skipf("the main thread also lists child %d, so this run proves nothing", child)
		}
	}

	info, ok := readProcStat(proc)
	if !ok {
		t.Fatalf("could not read the helper's own stat")
	}
	found := false
	for _, pid := range childrenOf(proc, info.threads) {
		if pid == child {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("childrenOf(%d) missed child %d, which is recorded against worker thread %d and not against the main task: a tree walk would under-report this process and could read a live agent as gone",
			proc, child, tid)
	}
}
