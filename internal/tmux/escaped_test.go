package tmux

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// board is a process table the escaped pass reads: one launch of session
// "s1" started at tick 1000, and the processes the live board carried.
func board() []escapedProc {
	return []escapedProc{
		// The sandbox the agent left behind: outer bwrap under init and the
		// socat proxy inside it.
		{pid: 10, ppid: 1, start: 1500, tag: "s1", args: "bwrap --new-session --die-with-parent"},
		{pid: 11, ppid: 10, start: 1501, tag: "s1", args: "socat TCP-LISTEN:3128,fork"},
		// The shared codex daemon, started from this session's pane.
		{pid: 20, ppid: 1, start: 1200, tag: "s1", args: "codex app-server-daemon"},
		{pid: 21, ppid: 20, start: 1201, tag: "s1", args: "codex worker"},
		// The opencode service other panes connect to.
		{pid: 30, ppid: 1, start: 1300, tag: "s1", args: "opencode serve --service"},
		// The stop worker, ending s1 under s1's id.
		{pid: 40, ppid: 1, start: 1900, tag: "s1", args: "gate-inbox _finish-stop 2026-11-06T03:00:00Z %5 77:1"},
		// An earlier life of the same row, before a revive.
		{pid: 50, ppid: 1, start: 900, tag: "s1", args: "Xvfb :99"},
		// Another session's escapee.
		{pid: 60, ppid: 1, start: 1600, tag: "s2", args: "Xvfb :100"},
		// A detached process of s1 still parented inside the live tree.
		{pid: 70, ppid: 5, start: 1700, tag: "s1", args: "sleep 600"},
		// The caller's own shell, tagged with the id it is ending.
		{pid: 80, ppid: 1, start: 1800, tag: "s1", args: "zsh"},
	}
}

func reaped(t *testing.T, spare map[int]bool) []int {
	t.Helper()
	got := escapedFrom(board(), "s1", 1000, spare)
	slices.Sort(got)
	return got
}

func TestEscapedReapsTheOrphanedSandboxTree(t *testing.T) {
	got := reaped(t, map[int]bool{80: true})
	if !slices.Contains(got, 10) || !slices.Contains(got, 11) {
		t.Fatalf("reaped %v, want the orphaned bwrap 10 and its socat 11", got)
	}
}

func TestEscapedSparesTheSharedCodexDaemon(t *testing.T) {
	got := reaped(t, nil)
	if slices.Contains(got, 20) || slices.Contains(got, 21) {
		t.Fatalf("reaped %v: the codex app-server daemon and its worker serve other sessions", got)
	}
}

func TestEscapedSparesTheOpencodeService(t *testing.T) {
	if got := reaped(t, nil); slices.Contains(got, 30) {
		t.Fatalf("reaped %v: opencode serve is shared by other panes", got)
	}
}

func TestEscapedSparesTheStopWorker(t *testing.T) {
	if got := reaped(t, nil); slices.Contains(got, 40) {
		t.Fatalf("reaped %v: the _finish-stop worker carries the id it is ending", got)
	}
}

func TestEscapedSparesAnEarlierLaunch(t *testing.T) {
	if got := reaped(t, nil); slices.Contains(got, 50) {
		t.Fatalf("reaped %v: pid 50 started before this launch", got)
	}
}

func TestEscapedSparesAnotherSessionsProcesses(t *testing.T) {
	if got := reaped(t, nil); slices.Contains(got, 60) {
		t.Fatalf("reaped %v: pid 60 is tagged with another session", got)
	}
}

func TestEscapedLeavesProcessesNotUnderInitToTheTreeWalk(t *testing.T) {
	if got := reaped(t, nil); slices.Contains(got, 70) {
		t.Fatalf("reaped %v: pid 70 has a living parent, so it is not an escapee", got)
	}
}

func TestEscapedSparesTheCaller(t *testing.T) {
	if got := reaped(t, map[int]bool{80: true}); slices.Contains(got, 80) {
		t.Fatalf("reaped %v: pid 80 is the caller or its ancestor", got)
	}
}

func TestEscapedNeedsALaunchToMeasureFrom(t *testing.T) {
	if got := escapedFrom(board(), "s1", 0, nil); got != nil {
		t.Fatalf("reaped %v with no launch start", got)
	}
	if got := escapedFrom(board(), "", 1000, nil); got != nil {
		t.Fatalf("reaped %v with no session id", got)
	}
}

func TestSpareSelfCoversThisProcessAndItsParent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the ancestor walk reads /proc")
	}
	spare := spareSelf()
	for _, pid := range []int{os.Getpid(), os.Getppid()} {
		if !spare[pid] {
			t.Fatalf("spare = %v, want %d", spare, pid)
		}
	}
	if spare[1] {
		t.Fatalf("spare = %v: init is never a candidate and need not be listed", spare)
	}
}

func TestTagOfFindsTheSessionID(t *testing.T) {
	env := []byte("HOME=/h\x00GATE_INBOX_SESSION_ID=abc123\x00PATH=/bin\x00")
	if got := tagOf(env); got != "abc123" {
		t.Fatalf("tagOf = %q", got)
	}
	if got := tagOf([]byte("GATE_INBOX_SESSION_ID_X=no\x00")); got != "" {
		t.Fatalf("tagOf matched a longer name: %q", got)
	}
}

// A tool call that double-forks leaves the window's tree before the window
// closes. The stop path must still end it when it carries the session's tag,
// and must not end the same kind of process tagged with another id.
func TestKillPaneReapsATaggedProcessThatEscapedTheTree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the escaped pass reads /proc")
	}
	driver := requireTmux(t)
	dir := t.TempDir()
	id := uniqueID("escape")
	mine := filepath.Join(dir, "mine.pid")
	theirs := filepath.Join(dir, "theirs.pid")
	// fork, setsid, fork again: the grandchild hangs off init, not the pane.
	escape := func(tag, file string) string {
		return `GATE_INBOX_SESSION_ID=` + tag + ` perl -e 'use POSIX; exit if fork; POSIX::setsid(); exit if fork; ` +
			`$SIG{HUP}="IGNORE"; open my $f, ">", $ARGV[0]; print $f $$; close $f; sleep 600' "` + file + `"`
	}
	script := filepath.Join(dir, "agent.sh")
	body := escape(id, mine) + "\n" + escape("someone-else", theirs) + "\nexec sleep 600\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := driver.Create(id, "/tmp", "sh "+script, nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	minePID, theirsPID := waitPID(t, mine), waitPID(t, theirs)
	t.Cleanup(func() {
		syscall.Kill(minePID, syscall.SIGKILL)
		syscall.Kill(theirsPID, syscall.SIGKILL)
	})

	pane, err := driver.PaneID(id)
	if err != nil {
		t.Fatalf("PaneID: %v", err)
	}
	out, err := driver.run("display-message", "-p", "-t", pane, "#{pane_pid}")
	if err != nil {
		t.Fatalf("pane_pid: %v", err)
	}
	if err := driver.KillPane(id, pane, strings.TrimSpace(out)); err != nil {
		t.Fatalf("KillPane: %v", err)
	}
	AwaitReaps()
	if !gone(minePID) {
		t.Fatalf("pid %d carried this session's tag and escaped the tree, and survived the stop", minePID)
	}
	if gone(theirsPID) {
		t.Fatalf("pid %d carried another session's tag and was reaped", theirsPID)
	}
}

func waitPID(t *testing.T, file string) int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		raw, _ := os.ReadFile(file)
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(raw))); pid > 0 {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never written", file)
		}
	}
}

func gone(pid int) bool {
	for deadline := time.Now().Add(time.Second); ; time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}
