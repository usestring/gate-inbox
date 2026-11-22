//go:build darwin

package hogs

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startTree runs script under sh in its own process group and kills the
// whole group when the test ends.
func startTree(t *testing.T, script string) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

// sampleOver samples roots twice, interval apart, and returns the second.
func sampleOver(t *testing.T, s *Sampler, roots map[string]int, interval time.Duration) Sample {
	t.Helper()
	s.Sample(time.Now(), roots)
	time.Sleep(interval)
	return s.Sample(time.Now(), roots)
}

func TestDarwinSamplerReadsALiveTree(t *testing.T) {
	s := NewSampler("")
	if !s.Supported() {
		t.Fatal("the libproc sampler is not supported on this Mac")
	}
	root := startTree(t, `while :; do :; done & sleep 60 & wait`)
	roots := map[string]int{"busy": root, "gone": 1 << 30}
	// The shell has to have started its children before the tree is worth
	// measuring.
	deadline := time.Now().Add(10 * time.Second)
	for s.Sample(time.Now(), roots).Sessions["busy"].Procs < 3 {
		if time.Now().After(deadline) {
			t.Fatal("the test tree never grew its two children")
		}
		time.Sleep(20 * time.Millisecond)
	}
	sample := sampleOver(t, s, roots, time.Second)

	if _, found := sample.Sessions["gone"]; found {
		t.Error("a root that does not exist got a usage")
	}
	usage, found := sample.Sessions["busy"]
	if !found {
		t.Fatal("the live tree got no usage")
	}
	if usage.Procs != 3 {
		t.Errorf("Procs = %d, want 3 (shell, busy loop, sleep)", usage.Procs)
	}
	// One busy loop is one core. A loaded machine can starve it, so the
	// floor only says it was seen working; the ceiling is what catches a
	// missed mach timebase conversion, which reads 41x high on Apple silicon.
	if !usage.CPUValid || usage.CPUPercent < 5 || usage.CPUPercent > 150 {
		t.Errorf("CPU = %.1f%% (valid %v), want about 100%%", usage.CPUPercent, usage.CPUValid)
	}
	if usage.MemBytes == 0 || usage.PSS {
		t.Errorf("MemBytes = %d, PSS = %v; want a footprint and no PSS", usage.MemBytes, usage.PSS)
	}
	if !sample.Host.OK || sample.Host.MemAvailable > sample.Host.MemTotal {
		t.Errorf("host memory %+v", sample.Host)
	}
	if len(usage.TopCPU) == 0 {
		t.Fatal("no process was named as using CPU")
	}
	s.Name(&usage)
	if top := usage.TopCPU[0]; !strings.HasPrefix(top.Command, "sh -c while :; do :; done") {
		t.Errorf("heaviest process %d is %q, want the busy-loop shell", top.PID, top.Command)
	}
}

// A tree whose work is all in children that start and exit between two
// samples must still read busy: their time reaches the tree through the
// reaped-children time of the shell that waited for them.
func TestDarwinSamplerCountsReapedChildren(t *testing.T) {
	s := NewSampler("")
	root := startTree(t, `while :; do sh -c 'i=0; while [ $i -lt 3000 ]; do i=$((i+1)); done'; done`)
	sample := sampleOver(t, s, map[string]int{"burst": root}, 2*time.Second)
	usage := sample.Sessions["burst"]
	// Without the reaped time this tree reads ~2.5%, the parent shell's own
	// forking; with it, about one core.
	if !usage.CPUValid || usage.CPUPercent < 25 || usage.CPUPercent > 150 {
		t.Errorf("CPU = %.1f%% (valid %v) for a tree of short-lived busy children, want about 100%%",
			usage.CPUPercent, usage.CPUValid)
	}
}

func TestDarwinNoticesOfferNoSystemd(t *testing.T) {
	body := Compose([]Alert{{Kind: KindCPU, Tier: TierWarn}, {Kind: KindMemory, Tier: TierWarn}}, Usage{}, Host{})
	if strings.Contains(body, "systemd") {
		t.Fatalf("a macOS notice suggests systemd, which macOS does not have:\n%s", body)
	}
}
