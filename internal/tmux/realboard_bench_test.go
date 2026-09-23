package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRealBoardCaptureCost compares the two ways a naming sweep can read its
// panes, against the operator's live board: one tmux fork per pane, versus the
// chained command list CaptureScrollback sends now -- one forked tmux per
// server with every pane inside it.
//
// Both sides fork, which is the point: a capture reply held by a control
// client is what the server never gives back (see the note above
// CaptureScrollback), so the saving here is the process count, not the
// transport. The byte totals in the log line are the correctness half -- a
// chained read that returned different text would show up there.
//
// Read-only on the operator's panes -- capture-pane only, never a key, a
// resize, or an attach to a pane this test did not create.
func TestRealBoardCaptureCost(t *testing.T) {
	if os.Getenv("BOARD_MEASURE") == "" {
		t.Skip("set BOARD_MEASURE=1 to measure the live board")
	}
	const socket = DefaultSocket

	out, err := exec.Command("tmux", "-L", socket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Skipf("no live board: %v", err)
	}
	var panes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			panes = append(panes, line)
		}
	}
	if len(panes) == 0 {
		t.Skip("no panes on the live board")
	}

	driver, err := NewWithSocket(socket)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	t.Cleanup(driver.CloseCaptureClients)

	const lines = 200
	for pass := 1; pass <= 3; pass++ {
		mark := time.Now()
		forked := 0
		for _, pane := range panes {
			args := []string{"-L", socket, "capture-pane", "-p", "-S", "-" + strconv.Itoa(lines), "-t", pane}
			if b, err := exec.Command("tmux", args...).Output(); err == nil {
				forked += len(b)
			}
		}
		forkTime := time.Since(mark)

		mark = time.Now()
		got := driver.CaptureScrollback(socket, panes, lines)
		batchTime := time.Since(mark)
		batched, failed := 0, 0
		for _, c := range got {
			if c.Err != nil {
				failed++
				continue
			}
			batched += len(c.Text)
		}

		fmt.Printf("pass %d  panes=%-3d fork=%-9s (%.2fms/pane)  batch=%-9s (%.2fms/pane)  speedup=%.1fx  forkKB=%d batchKB=%d failed=%d\n",
			pass, len(panes),
			forkTime.Round(time.Millisecond), float64(forkTime.Microseconds())/1000/float64(len(panes)),
			batchTime.Round(time.Millisecond), float64(batchTime.Microseconds())/1000/float64(len(panes)),
			float64(forkTime)/float64(batchTime),
			forked/1024, batched/1024, failed)
	}
}
