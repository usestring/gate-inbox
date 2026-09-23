package ui

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmux"
)

// The measurement the focused read path is designed around, kept so the
// numbers quoted in focusecho.go and tmux/pipe.go can be re-derived rather
// than believed.
//
// It asserts only the ordering that decided the design -- that asking over
// the pooled control pipe is at least an order of magnitude cheaper than
// forking a tmux process -- and logs the rest, because absolute times belong
// to whatever box this runs on. What it measured on the development box, on
// an adopted pane running cat:
//
//	fork capture-pane            mean 4.91ms   p50 3.28ms
//	fork send-keys               mean 2.99ms   p50 2.33ms
//	pooled-pipe capture-pane     mean 66µs     p50 45µs
//	pooled-pipe send-keys        mean 1µs
//	echo, fork send + 300ms tick mean 302ms    <- what the operator had
//	echo, pipe send + pipe chase mean 531µs    <- what replaced it
//
// Run it for the numbers:
//
//	go test ./internal/ui/ -run TestFocusedReadPathCosts -v
func TestFocusedReadPathCosts(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// A private socket standing in for the operator's own server: the pane
	// is one the manager did not create, which is the case the whole design
	// turns on.
	socket := newTestSocket()
	t.Cleanup(func() { tmuxOnSocket(socket, "kill-server").Run() })
	if out, err := tmuxOnSocket(socket, "new-session", "-d", "-s", "user-main", "cat").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	paneOut, err := tmuxOnSocket(socket, "display-message", "-p", "-t", "user-main", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("read pane id: %v", err)
	}
	pane := strings.TrimSpace(string(paneOut))

	driver := newTestDriver(t, socket)
	const id = "costs"
	if err := driver.Adopt(id, tmux.Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	_ = driver

	const n = 60
	var forkCap, forkSend, pipeCap, pipeSend []time.Duration
	for i := 0; i < n; i++ {
		start := time.Now()
		// The fork measured directly rather than through the driver: every
		// driver read prefers the pooled pipe now, so asking it for a fork
		// would need a hook in production code that exists only for this.
		if err := tmuxOnSocket(socket, "capture-pane", "-p", "-e", "-t", pane).Run(); err != nil {
			t.Fatalf("forked capture: %v", err)
		}
		forkCap = append(forkCap, time.Since(start))
	}
	control, err := driver.OpenPollControl(socket)
	if err != nil {
		t.Fatalf("OpenPollControl on the pane's own server: %v", err)
	}
	t.Cleanup(func() { control.Close() })
	for i := 0; i < n; i++ {
		start := time.Now()
		if _, err := control.Command("capture-pane -p -e -t " + pane); err != nil {
			t.Fatalf("pipe capture: %v", err)
		}
		pipeCap = append(pipeCap, time.Since(start))
	}
	for i := 0; i < n; i++ {
		start := time.Now()
		if err := control.Send("send-keys -t " + pane + " -H 78"); err != nil {
			t.Fatalf("pipe send: %v", err)
		}
		pipeSend = append(pipeSend, time.Since(start))
	}
	_ = forkSend

	t.Log(statLine("fork capture-pane", forkCap))
	t.Log(statLine("pooled-pipe capture-pane", pipeCap))
	t.Log(statLine("pooled-pipe send-keys (no wait)", pipeSend))

	// The whole design rests on this gap. If it ever closes, the chase in
	// focusecho.go is paying for itself with nothing and its cadences are
	// wrong.
	if mean(pipeCap)*10 > mean(forkCap) {
		t.Fatalf("a pooled-pipe capture (%v) is no longer an order of magnitude cheaper than a fork (%v)",
			mean(pipeCap), mean(forkCap))
	}

	// End to end, on the same pane: type a marker and look until it lands.
	var chase []time.Duration
	for i := 0; i < 25; i++ {
		marker := fmt.Sprintf("ZQ%03dZ", i)
		codes := make([]string, 0, len(marker))
		for _, b := range []byte(marker) {
			codes = append(codes, fmt.Sprintf("%02x", b))
		}
		start := time.Now()
		if err := control.Send("send-keys -t " + pane + " -H " + strings.Join(codes, " ")); err != nil {
			t.Fatalf("pipe send: %v", err)
		}
		for {
			out, err := control.Command("capture-pane -p -e -t " + pane)
			if err != nil {
				t.Fatalf("pipe capture: %v", err)
			}
			if strings.Contains(out, marker) {
				break
			}
			if time.Since(start) > 5*time.Second {
				t.Fatalf("the marker %q never reached the pane", marker)
			}
		}
		chase = append(chase, time.Since(start))
		control.Command("send-keys -t " + pane + " Enter")
		time.Sleep(20 * time.Millisecond)
	}
	t.Log(statLine("echo: pipe send + pipe chase", chase))
}

func mean(d []time.Duration) time.Duration {
	var sum time.Duration
	for _, v := range d {
		sum += v
	}
	return sum / time.Duration(len(d))
}

func statLine(label string, d []time.Duration) string {
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(q float64) time.Duration { return sorted[int(float64(len(sorted)-1)*q)] }
	return fmt.Sprintf("%-32s n=%d mean=%v p50=%v p95=%v max=%v",
		label, len(sorted), mean(sorted).Round(time.Microsecond),
		at(0.5).Round(time.Microsecond), at(0.95).Round(time.Microsecond), at(1).Round(time.Microsecond))
}

// The driver's own reads must take the pooled pipe, not merely be capable of
// it. The cost measurement above times the two paths side by side and would
// go on passing if every driver read quietly fell back to forking, which is
// the regression that would put the 300ms echo back without failing anything.
//
// One pooled client on the pane's own server is the evidence: a fork opens
// none, and a client per session -- the shape that killed the operator's tmux
// server -- would open more than one over several reads of several panes.
func TestDriverReadsGoOverThePooledPipe(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := newTestSocket()
	t.Cleanup(func() { tmuxOnSocket(socket, "kill-server").Run() })
	for _, name := range []string{"user-one", "user-two"} {
		if out, err := tmuxOnSocket(socket, "new-session", "-d", "-s", name, "cat").CombinedOutput(); err != nil {
			t.Fatalf("new-session %s: %v: %s", name, err, out)
		}
	}
	driver := newTestDriver(t, socket)
	for i, session := range []string{"user-one", "user-two"} {
		paneOut, err := tmuxOnSocket(socket, "display-message", "-p", "-t", session, "#{pane_id}").Output()
		if err != nil {
			t.Fatalf("read pane id: %v", err)
		}
		id := fmt.Sprintf("pooled%d", i)
		if err := driver.Adopt(id, tmux.Target{Socket: socket, Name: strings.TrimSpace(string(paneOut))}); err != nil {
			t.Fatalf("Adopt: %v", err)
		}
		for r := 0; r < 5; r++ {
			if _, err := driver.CapturePane(id); err != nil {
				t.Fatalf("CapturePane: %v", err)
			}
			if _, err := driver.PaneState(id, paneStateFormat); err != nil {
				t.Fatalf("PaneState: %v", err)
			}
		}
	}
	if got := driver.ControlClientsOn(socket); got != 1 {
		t.Fatalf("after twenty reads across two adopted panes the manager holds %d control clients on that server, want exactly 1 pooled one", got)
	}
	assertNoClientOnForeignSessions(t, socket)
}
