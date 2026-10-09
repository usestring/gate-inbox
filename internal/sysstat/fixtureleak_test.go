package sysstat

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTheProcessTreeFixtureOutlivesNothing is what keeps the leak from coming
// back. The fixtures were leaking a busy-loop shell per run for long enough
// that a board accumulated 25 of them, and nothing failed while it happened:
// the tests they belong to passed either way.
//
// The tree is built inside a subtest so the cleanup under test has actually
// run by the time the counts are compared -- t.Run returns only after the
// subtest's t.Cleanup stack unwinds.
func TestTheProcessTreeFixtureOutlivesNothing(t *testing.T) {
	before := fixtureProcs()

	var peak int
	t.Run("a measured tree", func(t *testing.T) {
		startFixture(t, `while :; do :; done & sh -c 'sleep 60' "$0" & wait`)
		// The shell, its forked busy-loop subshell and the marked sleeper
		// all have to be up before the count means anything.
		peak = awaitAtLeast(before+3, 10*time.Second)
	})

	// Without this the test passes on a board where the fixture never
	// started at all, which is how a guard quietly stops guarding.
	if peak <= before {
		t.Fatalf("the fixture never started (%d -> %d marked processes): this guard would pass no matter what leaked", before, peak)
	}

	after := awaitFixtureProcs(before, 10*time.Second)
	if after > before {
		t.Fatalf("the fixture leaked %d process(es): %d marked before, %d at peak, %d after cleanup. "+
			"A backgrounded child outlives the shell that started it, so cleanup has to kill the process group",
			after-before, before, peak, after)
	}
}

// awaitAtLeast waits for the marked-process count to reach want, and returns
// whatever it reached. Used to give the fixture's children time to appear
// rather than to assert: the assertion is in the caller.
func awaitAtLeast(want int, within time.Duration) int {
	deadline := time.Now().Add(within)
	for {
		got := fixtureProcs()
		if got >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fixtureOrphanEnv makes TestAKilledRunLeavesNoFixtureBehind's re-executed
// child start a fixture and kill itself before any cleanup runs.
const fixtureOrphanEnv = "GATE_INBOX_SYSSTAT_FIXTURE_ORPHAN"

// TestAKilledRunLeavesNoFixtureBehind is the case the cleanup above cannot
// reach: a test binary that dies by -timeout or SIGKILL runs no t.Cleanup, and
// the busy loop it started outlived it by six days.
func TestAKilledRunLeavesNoFixtureBehind(t *testing.T) {
	if os.Getenv(fixtureOrphanEnv) == "1" {
		cmd := startFixture(t, `while :; do :; done & sh -c 'sleep 60' "$0" & wait`)
		os.Stdout.WriteString("pgid=" + strconv.Itoa(cmd.Process.Pid) + "\n")
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {}
	}

	child := exec.Command(os.Args[0], "-test.run=^TestAKilledRunLeavesNoFixtureBehind$")
	child.Env = append(os.Environ(), fixtureOrphanEnv+"=1")
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Skipf("cannot re-exec the test binary: %v", err)
	}
	pgid := 0
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		if v, ok := strings.CutPrefix(scanner.Text(), "pgid="); ok {
			pgid, _ = strconv.Atoi(v)
		}
	}
	_ = child.Wait()
	if pgid == 0 {
		t.Fatal("the child never reported its fixture's process group")
	}
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })

	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group %d outlived the killed test binary that started it", pgid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
