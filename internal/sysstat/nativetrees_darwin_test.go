//go:build darwin

package sysstat

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The macOS reader must report the tree ps does: the same processes, the
// same children by name, and CPU and memory of the same magnitude. ps is
// read first and the native read second, and the burner child keeps
// running between them, so CPU is allowed to be a little ahead.
func TestNativeTreesAgreeWithPs(t *testing.T) {
	root := treeUnderTest(t)
	full := Trees([]int{root})[root]
	native, ok := nativeTrees([]int{root}, true)
	if !ok {
		t.Fatal("nativeTrees could not read the process table")
	}
	got := native[root]
	if !got.OK {
		t.Fatalf("native read did not resolve the tree: %+v", got)
	}
	if got.Procs != full.Procs {
		t.Errorf("native counted %d processes, ps counted %d", got.Procs, full.Procs)
	}
	if ratio := float64(got.RSS) / float64(full.RSS); ratio < 0.5 || ratio > 2 {
		t.Errorf("native RSS %d and ps RSS %d differ by more than 2x", got.RSS, full.RSS)
	}
	if full.CPUSeconds < 0.05 {
		t.Fatalf("the burner child never accumulated measurable CPU (ps saw %vs)", full.CPUSeconds)
	}
	if ratio := got.CPUSeconds / full.CPUSeconds; ratio < 0.5 || ratio > 2 {
		t.Errorf("native CPU %vs and ps CPU %vs (ratio %.3f) disagree: the timebase conversion is wrong",
			got.CPUSeconds, full.CPUSeconds, ratio)
	}
	if got.PCPU <= 0 {
		t.Errorf("lifetime PCPU = %v with a child burning a core", got.PCPU)
	}
	wantChildren := slices.Clone(full.Children)
	gotChildren := slices.Clone(got.Children)
	slices.Sort(wantChildren)
	slices.Sort(gotChildren)
	if !slices.Equal(gotChildren, wantChildren) {
		t.Errorf("native children %q, ps children %q", gotChildren, wantChildren)
	}
}

func TestNativeTreesSkipAGoneRoot(t *testing.T) {
	stats, ok := nativeTrees([]int{1 << 30}, false)
	if !ok {
		t.Fatal("nativeTrees could not read the process table")
	}
	if _, found := stats[1<<30]; found {
		t.Fatalf("a pid that does not exist got a sample: %+v", stats)
	}
}

func TestDarwinArgv0IsTheProgramAsInvoked(t *testing.T) {
	want := os.Args[0]
	if got := darwinArgv0(os.Getpid()); got != want && filepath.Base(got) != filepath.Base(want) {
		t.Fatalf("darwinArgv0(self) = %q, want %q", got, want)
	}
}

// The sampler's steady-state pass on macOS is the native read, not ps.
func TestSamplerUsesTheNativeReadAfterSeeding(t *testing.T) {
	root := treeUnderTest(t)
	sampler := NewTreeSampler("")
	seed := sampler.Sample([]int{root}, nil)[root]
	second := sampler.Sample([]int{root}, nil)[root]
	if !second.OK || second.Procs != seed.Procs {
		t.Fatalf("second sample %+v disagrees with the seed %+v", second, seed)
	}
	if second.PCPU != seed.PCPU {
		t.Errorf("second sample PCPU %v, want the seed's %v", second.PCPU, seed.PCPU)
	}
}

func BenchmarkTreesPs(b *testing.B) {
	roots := []int{os.Getppid()}
	for b.Loop() {
		Trees(roots)
	}
}

func BenchmarkTreesNative(b *testing.B) {
	roots := []int{os.Getppid()}
	for b.Loop() {
		nativeTrees(roots, false)
	}
}
