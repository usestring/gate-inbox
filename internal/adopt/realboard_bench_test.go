package adopt

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
)

// TestRealBoardSweepCost times the primitives a naming sweep runs, against the
// operator's live tmux board. Read-only throughout: list-panes and capture-pane
// only, never a key, a resize, or an attach.
//
// A fixture cannot answer this. The sweep's cost is a function of how many
// panes are up and how big the machine's process table is, and both are
// properties of a real board.
func TestRealBoardSweepCost(t *testing.T) {
	if os.Getenv("SWEEP_MEASURE") == "" {
		t.Skip("set SWEEP_MEASURE=1 to measure the live board")
	}
	const socket = "default"

	for pass := 1; pass <= 3; pass++ {
		start := time.Now()

		mark := time.Now()
		panes := Panes(socket)
		listing := time.Since(mark)

		mark = time.Now()
		procs := NewProcTable()
		table := time.Since(mark)

		mark = time.Now()
		var pids int
		for _, c := range panes {
			pids += len(procs.PIDs(int32(c.PID)))
		}
		walk := time.Since(mark)

		mark = time.Now()
		var bytes int
		for _, c := range panes {
			text, err := CaptureLines(socket, c.PaneID, 200)
			if err != nil {
				continue
			}
			bytes += len(text)
			convo.Normalize(text)
		}
		captures := time.Since(mark)

		fmt.Printf("pass %d  panes=%-3d total=%-9s listing=%-9s proctable=%-9s walk=%-9s captures=%-9s (%.1fms/pane) pids=%d KB=%d\n",
			pass, len(panes),
			time.Since(start).Round(time.Millisecond),
			listing.Round(time.Millisecond),
			table.Round(time.Millisecond),
			walk.Round(time.Millisecond),
			captures.Round(time.Millisecond),
			float64(captures.Microseconds())/1000/float64(max(len(panes), 1)),
			pids, bytes/1024)
	}
}
