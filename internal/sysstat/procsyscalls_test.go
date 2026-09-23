package sysstat

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// The whole point of the raw reader is a number that no benchmark reports:
// how many times a pass enters the kernel. That is only visible from outside
// the process, so it is measured by tracing a child that does nothing but
// walk, and by differencing two run lengths -- the fixed cost of starting a
// Go program is then subtracted out rather than guessed at.

const (
	probeModeEnv   = "SYSSTAT_WALK_PROBE"
	probeRootEnv   = "SYSSTAT_WALK_PROBE_ROOT"
	probePassesEnv = "SYSSTAT_WALK_PROBE_PASSES"
)

// TestWalkSyscallProbe is not a test. It is the child body the tracer runs:
// one walk of the given tree, repeated, over whichever reader was asked for.
func TestWalkSyscallProbe(t *testing.T) {
	mode := os.Getenv(probeModeEnv)
	if mode == "" {
		t.Skip("child body of TestTheRawWalkEntersTheKernelFarLessOften")
	}
	root, err := strconv.Atoi(os.Getenv(probeRootEnv))
	if err != nil {
		t.Fatalf("bad probe root: %v", err)
	}
	passes, err := strconv.Atoi(os.Getenv(probePassesEnv))
	if err != nil {
		t.Fatalf("bad probe pass count: %v", err)
	}
	visited := 0
	for i := 0; i < passes; i++ {
		if mode == "readfile" {
			visited += len(walkViaReadFile(root))
			continue
		}
		visited += len(walkViaRawReader(root))
	}
	if visited == 0 {
		t.Fatalf("the probe walked nothing under pid %d", root)
	}
}

// tracedSyscalls runs the probe child under strace and returns the total
// number of calls it made among the syscalls a file read can produce.
func tracedSyscalls(t *testing.T, strace, mode string, root, passes int) int {
	t.Helper()
	cmd := exec.Command(strace, "-f", "-c",
		"-e", "trace=openat,read,close,fstat,statx,newfstatat,fcntl,epoll_ctl,getdents64",
		os.Args[0], "-test.run=^TestWalkSyscallProbe$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		probeModeEnv+"="+mode,
		probeRootEnv+"="+strconv.Itoa(root),
		probePassesEnv+"="+strconv.Itoa(passes))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("strace could not trace the probe (%v):\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// The summary's last row is "<%time> <seconds> <usecs/call> <calls>
		// [<errors>] total", so the call count is the fourth column whether
		// or not anything failed.
		if len(fields) >= 5 && fields[len(fields)-1] == "total" {
			calls, err := strconv.Atoi(fields[3])
			if err != nil {
				t.Fatalf("unreadable strace total %q", line)
			}
			return calls
		}
	}
	t.Fatalf("strace printed no summary:\n%s", out)
	return 0
}

// perWalk is the marginal cost of one walk: the difference between a long run
// and a short one, divided by the walks between them. Everything the Go
// runtime does at startup appears in both and cancels.
func perWalk(t *testing.T, strace, mode string, root int) float64 {
	t.Helper()
	const short, long = 10, 110
	lo := tracedSyscalls(t, strace, mode, root, short)
	hi := tracedSyscalls(t, strace, mode, root, long)
	return float64(hi-lo) / float64(long-short)
}

// The measurement the change was made for. os.ReadFile spends 6.6 syscalls on
// a file the walk reads 3 bytes of interest out of -- an fstat to size a
// buffer, a failing epoll_ctl registering an unpollable file with the
// netpoller, and a handful of fcntls on top of the open, read and close the
// work needs. The raw reader pays 3, and the board's poll pass reads ~2,100
// files every two seconds.
func TestTheRawWalkEntersTheKernelFarLessOften(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	strace, err := exec.LookPath("strace")
	if err != nil {
		t.Skip("strace is how this counts syscalls")
	}
	root := treeUnderTest(t)

	viaReadFile := perWalk(t, strace, "readfile", root)
	viaRaw := perWalk(t, strace, "raw", root)
	t.Logf("one walk of a %d-process tree: os.ReadFile %.1f syscalls, raw reader %.1f syscalls (%.0f%% fewer)",
		len(walkViaRawReader(root)), viaReadFile, viaRaw, 100*(1-viaRaw/viaReadFile))

	if viaReadFile <= 0 || viaRaw <= 0 {
		t.Fatalf("the probe measured nothing: readfile %.1f, raw %.1f", viaReadFile, viaRaw)
	}
	// The saving is structural -- three syscalls a file against six and a
	// half -- and measures at ~69% fewer on this box. The bar is set well
	// below that: anything at or above three quarters of the old cost means
	// the walk has gone back through the runtime's file layer.
	if viaRaw > 0.75*viaReadFile {
		t.Errorf("the raw walk costs %.1f syscalls where the os.ReadFile walk costs %.1f: the reader is no longer going straight to the kernel",
			viaRaw, viaReadFile)
	}
}

// BenchmarkTreeWalk is the same comparison in wall time, on whatever tree the
// caller points it at: PROBE_ROOT for a pane of a real board (or 1 for every
// process on the machine), otherwise this test binary's own tree.
func BenchmarkTreeWalk(b *testing.B) {
	if !procChildren() {
		b.Skip("no /proc child lists on this machine")
	}
	root := os.Getpid()
	if fromEnv, err := strconv.Atoi(os.Getenv("PROBE_ROOT")); err == nil {
		root = fromEnv
	}
	b.Logf("walking %d pids under %d", len(walkViaRawReader(root)), root)
	for _, c := range []struct {
		name string
		walk func(int) []int
	}{{"os.ReadFile", walkViaReadFile}, {"raw", walkViaRawReader}} {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if len(c.walk(root)) == 0 {
					b.Fatalf("walk of %d found nothing", root)
				}
			}
		})
	}
}
