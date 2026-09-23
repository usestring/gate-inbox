package sysstat

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The fixtures here exist to be measured, so every one of them is a small
// process tree: a shell with a CPU burner and a sleeper under it. Killing the
// shell does not end that tree. `sh -c '... & ... & wait'` leaves its
// backgrounded children behind, they are reparented to init, and a busy loop
// that nothing waits for then runs until the machine is rebooted -- one board
// had accumulated 25 of them. Each is also a row in the table the poll pass's
// `procs` phase walks, which is that pass's largest phase.
//
// So a fixture is started in its own process group and the group is what
// cleanup kills. That holds whether or not the shell's `wait` ever returns --
// with an infinite-loop child it never does -- and whether or not the shell is
// still the parent of anything by then.

// fixtureMarker tags every process this test binary starts, so the leak guard
// can count them without matching on `sh` or on a script fragment that another
// run, another checkout, or the operator's own shell might also be running.
// The pid makes it unique to this process: a second checkout testing in
// parallel carries its own marker and neither guard sees the other's trees.
var fixtureMarker = fmt.Sprintf("gate-inbox-sysstat-fixture-%d", os.Getpid())

// fixtureCommand builds `sh -c <script> <marker>`, which puts the marker in
// $0. A shell that forks keeps its argv, so a subshell of the script carries
// the marker too, and a script wanting to mark a child of its own can pass
// "$0" down.
func fixtureCommand(script string, args ...string) *exec.Cmd {
	argv := append([]string{"-c", script, fixtureMarker}, args...)
	cmd := exec.Command("sh", argv...)
	// The whole point: a new group id, which is the only handle that still
	// names the children after their shell is gone.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// startFixture starts a marked process tree and registers the cleanup that
// ends it. The cleanup signals the process group rather than the pid, and does
// not depend on the shell exiting: the shell is a member of the group it was
// given, so one kill covers the shell, its subshells and its children.
func startFixture(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := fixtureCommand(script)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a process tree: %v", err)
	}
	registerFixtureCleanup(t, cmd)
	return cmd
}

// registerFixtureCleanup is startFixture's second half, split out for the one
// fixture that has to attach a stdin pipe before it starts.
func registerFixtureCleanup(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	pgid := cmd.Process.Pid // Setpgid makes the leader's pid the group id.
	t.Cleanup(func() {
		// Negative pid is the group. SIGKILL rather than SIGTERM because a
		// `sh -c` running a background job ignores a term aimed at the
		// group's shell often enough to matter, and nothing here has state
		// worth unwinding.
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		// Reap the leader so it does not sit as a zombie for the rest of
		// the run. The other group members were never this process's
		// direct children to wait for.
		_ = cmd.Wait()
	})
}

// fixtureProcs counts the live processes carrying this binary's marker. It
// reads `ps` rather than /proc children because the leak it is looking for is
// precisely a process that is no longer anybody's child here.
func fixtureProcs() int {
	out, err := exec.Command("ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue
		}
		// Matching the marker anywhere in the argv is safe in a way a
		// script-text match would not be: the marker names this pid's own
		// fixtures and nothing else on the machine.
		if strings.Contains(strings.Join(fields[1:], " "), fixtureMarker) {
			n++
		}
	}
	return n
}

// awaitFixtureProcs waits for the marked-process count to fall to want. A
// SIGKILL'd process stays in the table until the kernel has finished with it,
// so a count taken the instant a cleanup returns can read one that is already
// dead.
func awaitFixtureProcs(want int, within time.Duration) int {
	deadline := time.Now().Add(within)
	for {
		got := fixtureProcs()
		if got <= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}
