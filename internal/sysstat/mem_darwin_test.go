//go:build darwin

package sysstat

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestClampSwapCeiling(t *testing.T) {
	const gib = 1 << 30
	cases := []struct {
		name            string
		allocated, free uint64
		want            uint64
	}{
		{"allocation plus free disk", 3 * gib, 45 * gib, 48 * gib},
		{"capped at the kernel file limit", 6 * gib, 400 * gib, 100 * gib},
		{"never below what is allocated", 120 * gib, 0, 120 * gib},
	}
	for _, tc := range cases {
		if got := clampSwapCeiling(tc.allocated, tc.free); got != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestMemUsedFromLevel(t *testing.T) {
	const total = 24 << 30
	if got, want := memUsedFromLevel(total, 58), uint64(total/100*42); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
	if got := memUsedFromLevel(total, 100); got != 0 {
		t.Fatalf("fully available: got %d, want 0", got)
	}
}

func TestSampleMemoryMatchesKernelLevel(t *testing.T) {
	snap := Sample("/")
	if !snap.MemOK {
		t.Fatal("mem not ok")
	}
	level, err := unix.SysctlUint32("kern.memorystatus_level")
	if err != nil {
		t.Fatal(err)
	}
	if want := float64(100 - level); snap.MemPercent < want-5 || snap.MemPercent > want+5 {
		t.Fatalf("mem %.1f%% drifted from kernel estimate %.0f%%", snap.MemPercent, want)
	}
	if snap.MemTotal == 0 || snap.MemUsed > snap.MemTotal {
		t.Fatalf("bad mem bounds used=%d total=%d", snap.MemUsed, snap.MemTotal)
	}
}
