package sysstat

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Tree answers the focused pane's process line every 1.2 seconds, so it has
// to say what the ps-based Trees says about the same tree without forking ps
// to say it. A ps that only records it was run catches a fork.
func TestTreeAgreesWithTreesWithoutForkingPs(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists here; Tree is Trees")
	}
	root, _, _ := markedTree(t)
	want, ok := Trees([]int{root})[root]
	if !ok {
		t.Fatalf("Trees lost the fixture root %d", root)
	}

	dir := t.TempDir()
	forked := filepath.Join(dir, "forked")
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte("#!/bin/sh\ntouch "+forked+"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, ok := Tree(root)
	if _, err := os.Stat(forked); err == nil {
		t.Fatal("Tree forked ps")
	}
	if !ok || !got.OK {
		t.Fatalf("Tree(%d) = %+v, %v; want the live fixture", root, got, ok)
	}
	if got.Procs != want.Procs {
		t.Errorf("Procs = %d, ps counted %d", got.Procs, want.Procs)
	}
	if !slices.Equal(got.Children, want.Children) {
		t.Errorf("Children = %q, ps named %q", got.Children, want.Children)
	}
	// RSS comes from statm, the count ps reads too. stat's own count runs
	// about a tenth low on processes this small, which is what this bound
	// is tight enough to catch.
	if ratio := float64(got.RSS) / float64(want.RSS); ratio < 0.98 || ratio > 1.02 {
		t.Errorf("RSS = %d, ps read %d", got.RSS, want.RSS)
	}
	// ps rounds %cpu to a tenth, and the two reads are moments apart.
	if diff := math.Abs(got.PCPU - want.PCPU); diff > 0.5 {
		t.Errorf("PCPU = %.2f, ps read %.2f", got.PCPU, want.PCPU)
	}
	if _, ok := Tree(-1); ok {
		t.Error("Tree(-1) reported a process")
	}
}

// The focused pane's clock pays one of these every 1.2 seconds.
func BenchmarkFocusedPaneTree(b *testing.B) {
	pid := os.Getpid()
	b.Run("Trees", func(b *testing.B) {
		for b.Loop() {
			Trees([]int{pid})
		}
	})
	b.Run("Tree", func(b *testing.B) {
		for b.Loop() {
			Tree(pid)
		}
	})
}
