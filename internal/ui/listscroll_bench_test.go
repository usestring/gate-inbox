package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// BenchmarkListPreviewNotch is one wheel notch over the preview of a plain
// pane: the manager walks tmux's scrollback and paints the region it reads.
// Same span notchLatency measures under focus, so the two numbers are
// directly comparable -- scrolling a session you are only watching should
// not cost more than scrolling the one you are in.
func BenchmarkListPreviewNotch(b *testing.B) {
	forks := newForkCounter(b)
	m, sessID := listWithHistory(b, "benchlistnotch")
	deepen(b, m, sessID)
	m.pane.history = paneHistorySize(b, m, sessID)
	benchScrollBurst(b, m, forks)
}

// BenchmarkForwardedNotch is the case that made the feature feel slow: a
// pane whose application owns the mouse scrolls itself, so the notch is a
// write and the frame only exists once the application has drawn it.
//
// The span timed here is what the operator waits through -- the baseline
// read, the write, the chase that watches for the repaint, the update that
// lands it and the frame painted after. What it replaces is not a faster
// version of itself but a scheduled capture: before the chase, the frame
// came on the next preview tick, previewIntervalLive at best and
// previewIntervalCalm on a row that reads as idle, which is the row most
// worth scrolling back through. Those two constants are the bar, and they
// are logged beside the measurement.
//
// The pane runs a loop that prints a line per byte it is sent, standing in
// for an agent CLI's own redraw. That keeps the measurement to the
// manager's path: a real agent's repaint is its own cost, and it is the
// same cost whether the frame is chased or waited for.
func BenchmarkForwardedNotch(b *testing.B) {
	m, sessID := listWithHistory(b, "benchforward")
	// The stand-in claims the mouse the way an agent CLI does and prints a
	// line per byte it is handed, so tmux reports it as mouse-owning for
	// real rather than the benchmark asserting it.
	script := filepath.Join(b.TempDir(), "mouseloop.sh")
	body := "printf '\033[?1000h\033[?1006h'\n" +
		"while IFS= read -r -s -n1 k; do printf 'notch %s\n' \"$(date +%s%N)\"; done\n"
	if err := os.WriteFile(script, []byte("#!/bin/bash\n"+body), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := m.tmux.SendText(sessID, "bash "+script); err != nil {
		b.Fatalf("SendText: %v", err)
	}
	// The loop has to be reading before the first report, or the bytes land
	// on a shell prompt and the pane answers with something else entirely.
	deadline := time.Now().Add(10 * time.Second)
	for !m.pane.mouse {
		if time.Now().After(deadline) {
			b.Fatal("the stand-in never claimed the mouse")
		}
		time.Sleep(50 * time.Millisecond)
		var facts paneFacts
		applyPaneState(&facts, readPaneState(b, m, sessID))
		m.storePaneState(sessID, facts)
	}
	seedLive(b, m, sessID)
	m.frame()
	if !m.pane.box.ok {
		b.Fatal("no preview box to aim at")
	}

	x, y := previewCell(m)
	var samples []time.Duration
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		was := m.preview
		start := time.Now()
		updated, cmd := m.handleMouse(wheel(i%2 == 0, x, y))
		m = updated.(*Model)
		if cmd == nil {
			b.Fatalf("a forwarded notch scheduled no look (pane.mouse=%v history=%d)",
				m.pane.mouse, m.pane.history)
		}
		drain(m, cmd)
		m.frame()
		took := time.Since(start)
		if m.preview == was {
			// The chase ran its budget out without the pane moving; timing a
			// frame that never arrived would flatter the result.
			continue
		}
		samples = append(samples, took)
	}
	b.StopTimer()
	reportLatency(b, samples, "notch")
	b.Logf("replaces a scheduled capture at previewIntervalLive=%v / previewIntervalCalm=%v",
		previewIntervalLive, previewIntervalCalm)
}

// BenchmarkPreviewCapture splits what one preview capture costs. The text is
// a single tmux call; the process tree behind the pane is a walk of /proc per
// process, and it used to ride along with every capture -- including the one
// a cursor move schedules, which is why a run of moves felt like the board
// had stopped answering.
func BenchmarkPreviewCapture(b *testing.B) {
	m, sessID := listWithHistory(b, "benchcapture")
	sess, ok := m.selected()
	if !ok || sess.ID != sessID {
		b.Fatal("no session selected")
	}
	for _, tc := range []struct {
		name     string
		withProc bool
	}{{"withProcessTree", true}, {"textOnly", false}} {
		b.Run(tc.name, func(b *testing.B) {
			var samples []time.Duration
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cmd := m.previewCmd(sess, m.previewGen, tc.withProc)
				start := time.Now()
				if msg := cmd(); msg == nil {
					b.Fatal("capture returned nothing")
				}
				samples = append(samples, time.Since(start))
			}
			b.StopTimer()
			reportLatency(b, samples, "capture")
		})
	}
}

// reportLatency is reportScroll without the fork tally, for spans whose cost
// is the wait rather than the processes behind it.
func reportLatency(b *testing.B, samples []time.Duration, unit string) {
	b.Helper()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	pct := func(p float64) float64 {
		if len(samples) == 0 {
			return 0
		}
		return float64(samples[int(float64(len(samples)-1)*p)].Microseconds()) / 1000
	}
	b.ReportMetric(pct(0.5), "p50ms/"+unit)
	b.ReportMetric(pct(0.9), "p90ms/"+unit)
	b.Logf("samples=%d p50=%.2fms p90=%.2fms", len(samples), pct(0.5), pct(0.9))
	if load, err := os.ReadFile("/proc/loadavg"); err == nil {
		b.Logf("loadavg %s", strings.TrimSpace(string(load)))
	}
}

// readPaneState is one display-message read of the pane facts, for a
// benchmark waiting on the pane's own claim rather than asserting it.
func readPaneState(b *testing.B, m *Model, sessID string) string {
	b.Helper()
	state, err := m.tmux.PaneState(sessID, paneStateFormat)
	if err != nil {
		b.Fatalf("pane state: %v", err)
	}
	return state
}
