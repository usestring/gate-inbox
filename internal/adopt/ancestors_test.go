package adopt

import (
	"os"
	"testing"
)

func TestAncestorsStartsAtTheParent(t *testing.T) {
	got := Ancestors(int32(os.Getpid()))
	if len(got) == 0 || got[0] != os.Getppid() {
		t.Fatalf("Ancestors = %v, want it to start at the parent %d", got, os.Getppid())
	}
	if len(got) > ancestorDepth {
		t.Fatalf("Ancestors walked %d levels, past the bound of %d", len(got), ancestorDepth)
	}
}
