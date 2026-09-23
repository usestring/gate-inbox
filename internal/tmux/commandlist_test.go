package tmux

import (
	"strings"
	"testing"
)

// The bug this splits for: the caller bounded a batch by how many sessions it
// covered, the bytes came from the labels, and a fleet gave its sessions long
// labels one day and put
// 32 sessions' worth of chrome at 18,576 bytes -- over tmux's ceiling, so
// tmux ran none of it and answered "command too long".
func TestSplitCommandListCutsByBytesNotByCount(t *testing.T) {
	label := strings.Repeat("x", 500)
	var commands [][]string
	for i := 0; i < 100; i++ {
		commands = append(commands, []string{"set-option", "-t", "gi_abcd1234", "status-left", label})
	}

	lists := splitCommandList(commands)
	if len(lists) < 2 {
		t.Fatalf("100 commands of ~520 bytes came back as %d list(s); they do not fit one", len(lists))
	}
	var seen int
	for _, list := range lists {
		size := 0
		for _, command := range list {
			size += len(command) + 1
			for _, arg := range command {
				size += len(arg)
			}
		}
		if size > commandListBudget {
			t.Fatalf("a list is %d bytes, over the %d budget", size, commandListBudget)
		}
		seen += len(list)
	}
	if seen != len(commands) {
		t.Fatalf("the lists hold %d commands, want all %d", seen, len(commands))
	}
}

// Order is the contract: chrome sets a session's status-left and then renames
// its window, and a split that reordered them would leave the tab named from
// whichever list happened to run last.
func TestSplitCommandListKeepsOrder(t *testing.T) {
	var commands [][]string
	for _, name := range []string{"first", "second", "third"} {
		commands = append(commands, []string{"set-option", name})
	}

	var flat []string
	for _, list := range splitCommandList(commands) {
		for _, command := range list {
			flat = append(flat, command[1])
		}
	}
	if strings.Join(flat, ",") != "first,second,third" {
		t.Fatalf("order = %v, want first,second,third", flat)
	}
}

// One command bigger than the whole budget cannot be made to fit, so it goes
// alone rather than dragging its neighbours into the failure with it: the
// caller's per-item retry is then the thing that names it.
func TestSplitCommandListIsolatesAnOversizedCommand(t *testing.T) {
	huge := []string{"set-option", strings.Repeat("y", commandListBudget+1)}
	commands := [][]string{{"set-option", "before"}, huge, {"set-option", "after"}}

	lists := splitCommandList(commands)
	if len(lists) != 3 {
		t.Fatalf("got %d lists, want the oversized command on its own between the other two", len(lists))
	}
	if len(lists[1]) != 1 || lists[1][0][1] != huge[1] {
		t.Fatalf("the middle list is not the oversized command alone: %v", lists[1])
	}
}
