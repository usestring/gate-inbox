package hogs

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeProc is a /proc tree on disk: the files the sampler reads and nothing
// else, rewritten between samples to stand for time passing.
type fakeProc struct {
	t    *testing.T
	root string
	// noChildren leaves out task/<tid>/children, as on a kernel built
	// without CONFIG_PROC_CHILDREN.
	noChildren bool
}

type fakeProcess struct {
	pid, ppid int
	comm      string
	cmdline   []string
	// utime, stime, cutime, cstime and start are in clock ticks.
	utime, stime, cutime, cstime, start uint64
	rssPages                            uint64
	// pssKiB, when set, writes smaps_rollup.
	pssKiB   uint64
	children []int
}

func newFakeProc(t *testing.T) *fakeProc {
	t.Helper()
	f := &fakeProc{t: t, root: t.TempDir()}
	f.host(16<<20, 8<<20)
	f.uptime(1000)
	return f
}

func (f *fakeProc) write(rel, body string) {
	f.t.Helper()
	path := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeProc) uptime(seconds float64) {
	f.write("uptime", fmt.Sprintf("%.2f 0.00\n", seconds))
}

func (f *fakeProc) host(totalKiB, availKiB uint64) {
	f.write("meminfo", fmt.Sprintf("MemTotal:       %d kB\nMemFree:          1 kB\nMemAvailable:   %d kB\n", totalKiB, availKiB))
}

func (f *fakeProc) put(p fakeProcess) {
	pid := strconv.Itoa(p.pid)
	// The comm carries a space and a parenthesis, which a left-to-right
	// split of the line would walk off into the wrong columns.
	stat := fmt.Sprintf("%d (%s) S %d 0 0 0 -1 0 0 0 0 0 %d %d %d %d 20 0 1 0 %d 0 %d 0 0\n",
		p.pid, p.comm, p.ppid, p.utime, p.stime, p.cutime, p.cstime, p.start, p.rssPages)
	f.write(pid+"/stat", stat)
	f.write(pid+"/cmdline", strings.Join(p.cmdline, "\x00")+"\x00")
	if p.pssKiB > 0 {
		f.write(pid+"/smaps_rollup", fmt.Sprintf("00400000-7fff [rollup]\nRss:  %d kB\nPss:  %d kB\n", p.rssPages*4, p.pssKiB))
	}
	if f.noChildren {
		if err := os.MkdirAll(filepath.Join(f.root, pid, "task", pid), 0o755); err != nil {
			f.t.Fatal(err)
		}
		return
	}
	var kids []string
	for _, c := range p.children {
		kids = append(kids, strconv.Itoa(c))
	}
	f.write(pid+"/task/"+pid+"/children", strings.Join(kids, " "))
}

func (f *fakeProc) remove(pid int) {
	if err := os.RemoveAll(filepath.Join(f.root, strconv.Itoa(pid))); err != nil {
		f.t.Fatal(err)
	}
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.5 {
		t.Fatalf("%s = %.2f, want %.2f", what, got, want)
	}
}

func newTestSampler(root string) *Sampler {
	s := NewSampler(root)
	s.pageSize = 4096
	return s
}

func TestSamplerSumsTheWholeTreeAndNamesTheHeaviest(t *testing.T) {
	for _, noChildren := range []bool{false, true} {
		t.Run(fmt.Sprintf("noChildren=%v", noChildren), func(t *testing.T) {
			f := newFakeProc(t)
			f.noChildren = noChildren
			shell := fakeProcess{pid: 10, ppid: 1, comm: "zsh", cmdline: []string{"zsh"}, rssPages: 256, children: []int{11}}
			agent := fakeProcess{pid: 11, ppid: 10, comm: "claude", cmdline: []string{"claude", "--settings", "x"}, rssPages: 1024, pssKiB: 2048, children: []int{12}}
			build := fakeProcess{pid: 12, ppid: 11, comm: "go (build)", cmdline: []string{"go", "build", "./..."}, rssPages: 2048, children: []int{13}}
			test := fakeProcess{pid: 13, ppid: 12, comm: "test.bin", cmdline: []string{"./test.bin", "-test.v"}, rssPages: 512}
			other := fakeProcess{pid: 50, ppid: 1, comm: "unrelated", utime: 99999, rssPages: 99999}
			for _, p := range []fakeProcess{shell, agent, build, test, other} {
				f.put(p)
			}
			s := newTestSampler(f.root)
			t0 := time.Unix(1_000_000, 0)
			first := s.Sample(t0, map[string]int{"sess": 10})
			usage := first.Sessions["sess"]
			if usage.CPUValid {
				t.Fatal("first sample reported CPU with nothing to difference against")
			}
			if usage.Procs != 4 {
				t.Fatalf("Procs = %d, want the 4 in the tree and not the unrelated one", usage.Procs)
			}
			// PSS where smaps_rollup exists, RSS pages elsewhere.
			wantMem := uint64(256*4096 + 2048*1024 + 2048*4096 + 512*4096)
			if usage.MemBytes != wantMem {
				t.Fatalf("MemBytes = %d, want %d", usage.MemBytes, wantMem)
			}
			if usage.TopMem[0].PID != 12 || usage.TopMem[0].Command != "go build ./..." {
				t.Fatalf("TopMem[0] = %+v, want the go build", usage.TopMem[0])
			}
			if !first.Host.OK || first.Host.AvailablePercent() != 50 {
				t.Fatalf("host = %+v, want 50%% available", first.Host)
			}

			// Ten seconds on: the build burns 3 cores and the tests 1.
			build.utime, build.stime = 2500, 500
			test.utime = 1000
			f.put(build)
			f.put(test)
			f.uptime(1010)
			second := s.Sample(t0.Add(10*time.Second), map[string]int{"sess": 10})
			usage = second.Sessions["sess"]
			if !usage.CPUValid {
				t.Fatal("second sample has no CPU")
			}
			near(t, "tree CPU", usage.CPUPercent, 400)
			if got := usage.TopCPU[0]; got.PID != 12 {
				t.Fatalf("TopCPU[0] = %+v, want the build", got)
			}
			near(t, "build CPU", usage.TopCPU[0].CPUPercent, 300)
			if len(usage.TopCPU) != 2 {
				t.Fatalf("TopCPU = %+v, want only the two processes that ran", usage.TopCPU)
			}
		})
	}
}

// A compiler that starts and exits between two samples is never seen alive,
// but its time lands in its parent's reaped counters and still counts. One
// that was seen and then reaped is counted once, not twice.
func TestSamplerCountsProcessesThatComeAndGoBetweenSamples(t *testing.T) {
	f := newFakeProc(t)
	shell := fakeProcess{pid: 10, ppid: 1, comm: "zsh", children: []int{11}}
	make1 := fakeProcess{pid: 11, ppid: 10, comm: "make", start: 1000, children: []int{12}}
	cc := fakeProcess{pid: 12, ppid: 11, comm: "cc", start: 99000, utime: 500}
	for _, p := range []fakeProcess{shell, make1, cc} {
		f.put(p)
	}
	s := newTestSampler(f.root)
	t0 := time.Unix(1_000_000, 0)
	s.Sample(t0, map[string]int{"sess": 10})

	// Interval 1: cc burns 5 more seconds and is reaped by make (whose
	// reaped time gains cc's whole 10s life); a second compiler starts and
	// exits unseen, adding another 5s to make's reaped time.
	f.remove(12)
	make1.children = nil
	make1.cutime = 1000 + 500
	f.put(make1)
	f.uptime(1010)
	u := s.Sample(t0.Add(10*time.Second), map[string]int{"sess": 10}).Sessions["sess"]
	near(t, "tree CPU", u.CPUPercent, 100)

	// Interval 2: a new process starts after the last sample and is seen
	// alive; all its time is this interval's.
	fresh := fakeProcess{pid: 13, ppid: 11, comm: "cc", start: 101500, utime: 300}
	make1.children = []int{13}
	f.put(make1)
	f.put(fresh)
	f.uptime(1020)
	u = s.Sample(t0.Add(20*time.Second), map[string]int{"sess": 10}).Sessions["sess"]
	near(t, "tree CPU with a new process", u.CPUPercent, 30)

	// Interval 3: a process leaves the tree without being reaped in it
	// (reparented away). Its lifetime must not be subtracted from real work.
	f.remove(13)
	make1.children = nil
	make1.utime = 200
	f.put(make1)
	f.uptime(1030)
	u = s.Sample(t0.Add(30*time.Second), map[string]int{"sess": 10}).Sessions["sess"]
	near(t, "tree CPU after a process left", u.CPUPercent, 20)
}

func TestSamplerTreatsARecycledPIDAsANewProcess(t *testing.T) {
	f := newFakeProc(t)
	shell := fakeProcess{pid: 10, ppid: 1, comm: "zsh", children: []int{11}}
	old := fakeProcess{pid: 11, ppid: 10, comm: "old", start: 500, utime: 90000}
	f.put(shell)
	f.put(old)
	s := newTestSampler(f.root)
	t0 := time.Unix(1_000_000, 0)
	s.Sample(t0, map[string]int{"sess": 10})
	// Same pid, a lower CPU counter: a different process that started after
	// the last sample.
	f.put(fakeProcess{pid: 11, ppid: 10, comm: "new", start: 100500, utime: 200})
	f.uptime(1010)
	u := s.Sample(t0.Add(10*time.Second), map[string]int{"sess": 10}).Sessions["sess"]
	near(t, "tree CPU", u.CPUPercent, 20)
}

func TestSamplerDropsSessionsWhoseRootIsGone(t *testing.T) {
	f := newFakeProc(t)
	f.put(fakeProcess{pid: 10, ppid: 1, comm: "zsh"})
	s := newTestSampler(f.root)
	t0 := time.Unix(1_000_000, 0)
	got := s.Sample(t0, map[string]int{"a": 10, "b": 77})
	if _, ok := got.Sessions["b"]; ok {
		t.Fatal("a session whose root pid has no /proc entry was reported")
	}
	f.remove(10)
	got = s.Sample(t0.Add(time.Second), map[string]int{"a": 10})
	if len(got.Sessions) != 0 || len(s.trees) != 0 {
		t.Fatalf("an exited tree is still held: %+v / %d memos", got.Sessions, len(s.trees))
	}
}

func TestSamplerQuotesACommandBoundedAndFallsBackToComm(t *testing.T) {
	f := newFakeProc(t)
	long := strings.Repeat("x", 400)
	f.put(fakeProcess{pid: 10, ppid: 1, comm: "zsh", cmdline: []string{"node", long}, children: []int{11}})
	f.put(fakeProcess{pid: 11, ppid: 10, comm: "kworker (x)"})
	f.write("11/cmdline", "")
	s := newTestSampler(f.root)
	if got := s.command(10); len([]rune(got)) != commandWidth || !strings.HasPrefix(got, "node x") {
		t.Fatalf("command = %q (%d runes), want it cut to %d", got, len([]rune(got)), commandWidth)
	}
	if got := s.command(11); got != "[kworker (x)]" {
		t.Fatalf("command = %q, want the comm for a process with no argv", got)
	}
}
