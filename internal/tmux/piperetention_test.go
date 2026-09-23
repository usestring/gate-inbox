package tmux

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Does a capture over the pooled control pipe actually retain memory in a
// tmux server that is being read from promptly, and if it does, is that
// retention something the allocator can be told to give back?
//
// The note above CaptureScrollback says captures were moved off the pipe
// because a 36-pane board polled every two seconds grew this box's server
// ~33 GiB/day, measured at 7.39 kB retained per capture against 0.01 kB
// forked. It used to name the mechanism too: the bytes are freed to glibc
// but the arena is never returned to the OS. That is an allocator-behaviour
// claim rather than a tmux-protocol one, and if it were right the fix would
// be a malloc knob rather than a fork.
//
// It is not right, and this is what says so. The answer is in the shape of
// the curve and in what does not move it.
//
// It matters because the pipe is 20-25x cheaper per capture than a fork
// (BenchmarkCaptureControlPipe against BenchmarkCaptureExecFork), and the
// fork is what the focused pane's whole cost is made of. If the retention is
// trimmable, captures can go back on the pipe on the tmux this box already
// runs.
//
// Skipped by default: it starts its own server, runs thousands of captures
// and takes a couple of minutes. GATE_INBOX_ARENA_PROBE=1 to run it.
func TestCaptureRetentionOverThePipe(t *testing.T) {
	if os.Getenv("GATE_INBOX_ARENA_PROBE") == "" {
		t.Skip("GATE_INBOX_ARENA_PROBE unset")
	}
	for _, tc := range []struct {
		name string
		env  []string
		rows int
	}{
		{name: "default allocator"},
		// M_TRIM_THRESHOLD_ caps how much free top-of-heap glibc keeps before
		// returning it; ARENA_MAX stops per-thread arenas multiplying the
		// high-water mark. If the retention is fragmentation rather than a
		// live backlog, this is what would give it back.
		{name: "trim-tuned allocator", env: []string{
			"MALLOC_TRIM_THRESHOLD_=131072",
			"MALLOC_MMAP_THRESHOLD_=131072",
			"MALLOC_ARENA_MAX=2",
		}},
		// Four times the screen. If retention tracks the size of the reply,
		// the bytes being kept are the reply text itself, and the per-capture
		// figure is a property of the pane rather than a constant.
		{name: "4x pane height", rows: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			perCapture, first, last, n := measureRetention(t, tc.env, tc.rows)
			t.Logf("%s: RSS %d kB -> %d kB over %d captures = %.2f kB/capture",
				tc.name, first, last, n, perCapture)
			// Reported, not asserted. The number is the finding; a threshold
			// here would be inventing a bar nobody has agreed on.
		})
	}
}

// measureRetention runs captures over one pooled control client against a
// server started with env, and reports kB of server RSS retained per capture.
func measureRetention(t *testing.T, env []string, rows int) (perCapture float64, first, last, n int) {
	if rows == 0 {
		rows = 50
	}
	t.Helper()
	socket := "arena" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	driver, err := NewWithSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	// The server inherits the allocator settings from whoever starts it, so
	// the very first command has to carry them.
	if len(env) > 0 {
		for _, kv := range env {
			parts := strings.SplitN(kv, "=", 2)
			t.Setenv(parts[0], parts[1])
		}
	}
	id := "arena" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	if err := driver.Create(id, "/tmp", "", nil, 200, rows); err != nil {
		t.Fatal(err)
	}
	defer driver.Kill(id)
	defer driver.run("kill-server")

	control, err := driver.OpenPollControl(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	pid := serverPID(t, driver, socket)
	// A null result from a manipulation that never reached the server is not
	// a null result. Read the server's own environment back.
	if len(env) > 0 {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		if err != nil {
			t.Fatal(err)
		}
		for _, kv := range env {
			if !strings.Contains(strings.ReplaceAll(string(raw), "\x00", "\n"), kv) {
				t.Fatalf("server %d did not inherit %s; this arm would measure nothing", pid, kv)
			}
		}
		t.Logf("    server %d inherited %v", pid, env)
	}
	// Fill the pane so each reply carries a real screen of text rather than
	// fifty blank lines: what the note blames is the size of the replies.
	for i := 0; i < rows; i++ {
		if _, err := control.Command(fmt.Sprintf("send-keys -t gi_%s 'line %d padding padding padding padding' Enter", id, i)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(500 * time.Millisecond)

	const captures = 10000
	// Settle first: the early growth is the server's own startup, not the
	// captures, and counting it would flatter or damn whichever arm runs first.
	for i := 0; i < 200; i++ {
		if _, err := control.Command("capture-pane -p -e -t gi_" + id); err != nil {
			t.Fatal(err)
		}
	}
	first = rssKB(t, pid)
	for i := 1; i <= captures; i++ {
		if _, err := control.Command("capture-pane -p -e -t gi_" + id); err != nil {
			t.Fatal(err)
		}
		if i%5000 == 0 {
			t.Logf("    %5d captures: server RSS %d kB", i, rssKB(t, pid))
		}
	}
	last = rssKB(t, pid)
	return float64(last-first) / float64(captures), first, last, captures
}

func serverPID(t *testing.T, d *Driver, socket string) int {
	t.Helper()
	out, err := d.run("display-message", "-p", "#{pid}")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("server pid %q: %v", out, err)
	}
	return pid
}

func rssKB(t *testing.T, pid int) int {
	t.Helper()
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			kb, err := strconv.Atoi(fields[1])
			if err != nil {
				t.Fatal(err)
			}
			return kb
		}
	}
	t.Fatalf("no VmRSS for pid %d", pid)
	return 0
}
