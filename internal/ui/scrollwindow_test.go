package ui

import (
	"fmt"
	"testing"
)

// sweepWindow steps a cursor down a list and back up, the way held j and k
// do, with the cursor's entry boxed two lines taller than the rest. It
// reports every step whose window moved although the previous one still
// held the cursor and its margin whole, or moved against the step.
func sweepWindow(window func(heights []int, cursor, budget, top int) (int, int), base []int, budget int) []string {
	var path []int
	for i := range base {
		path = append(path, i)
	}
	for i := len(base) - 2; i >= 0; i-- {
		path = append(path, i)
	}
	heightsAt := func(cursor int) []int {
		heights := append([]int(nil), base...)
		heights[cursor] += 2
		return heights
	}
	var bad []string
	start, _ := window(heightsAt(0), 0, budget, 0)
	for step := 1; step < len(path); step++ {
		cursor, previous := path[step], path[step-1]
		heights := heightsAt(cursor)
		lo, hi := max(cursor-scrollMargin, 0), min(cursor+scrollMargin, len(heights)-1)
		used := 0
		for i := start; i <= hi && i < len(heights); i++ {
			used += heights[i]
		}
		stillFits := lo >= start && used <= budget-1
		nextStart, nextEnd := window(heights, cursor, budget, start)
		if cursor < nextStart || cursor >= nextEnd {
			bad = append(bad, fmt.Sprintf("cursor %d outside [%d,%d)", cursor, nextStart, nextEnd))
		}
		if stillFits && nextStart != start {
			bad = append(bad, fmt.Sprintf("step %d->%d moved start %d->%d with the cursor in view", previous, cursor, start, nextStart))
		}
		if (cursor > previous && nextStart < start) || (cursor < previous && nextStart > start) {
			bad = append(bad, fmt.Sprintf("step %d->%d moved start %d->%d against the step", previous, cursor, start, nextStart))
		}
		start = nextStart
	}
	return bad
}

func TestLineWindowScrollsOnlyAtTheEdges(t *testing.T) {
	// Groups and sessions at two lines, artifacts and heads at one.
	base := []int{2, 2, 1, 1, 2, 2, 2, 1, 2, 2, 1, 2, 2, 2, 1, 1, 2, 2, 2, 2, 1, 2}
	for _, budget := range []int{9, 12, 17, 24} {
		if bad := sweepWindow(lineWindow, base, budget); len(bad) > 0 {
			t.Errorf("budget %d: the window jumped:\n%v", budget, bad)
		}
	}
}

// A poll that adds a row above the window must not slide it: the top entry is
// held by identity, not by index.
func TestListWindowHoldsItsTopAcrossAnInsert(t *testing.T) {
	heights := make([]int, 30)
	for i := range heights {
		heights[i] = 2
	}
	start, _ := lineWindow(heights, 17, 12, 15)
	if start != 15 {
		t.Fatalf("start = %d want the 15 the last frame drew", start)
	}
	// One row more above: the same entry is now at 16 and the cursor at 18.
	heights = append(heights, 2)
	if again, _ := lineWindow(heights, 18, 12, 16); again != 16 {
		t.Fatalf("start after an insert = %d want 16", again)
	}
}
