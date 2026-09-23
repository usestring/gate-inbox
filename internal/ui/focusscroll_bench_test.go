package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// forkCounter replaces tmux on PATH with a shim that records every
// invocation, so a benchmark can report processes per wheel notch rather
// than inferring them from wall clock.
type forkCounter struct{ path string }

func newForkCounter(b *testing.B) *forkCounter {
	b.Helper()
	binary, err := exec.LookPath("tmux")
	if err != nil {
		b.Skip("tmux not installed")
	}
	dir := b.TempDir()
	tally := filepath.Join(dir, "forks")
	script := "#!/bin/sh\nprintf 'x\\n' >> " + tally + "\nexec " + binary + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		b.Fatal(err)
	}
	b.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &forkCounter{path: tally}
}

func (f *forkCounter) count(b *testing.B) int {
	b.Helper()
	data, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		b.Fatal(err)
	}
	return strings.Count(string(data), "x\n")
}

// notchLatency is one wheel notch end to end: the model work the event loop
// does, the capture its command performs when the frame is not already in
// hand, the update that lands the reply, and the frame Bubble Tea paints
// after each of those messages. That whole span is what the operator waits
// through before the screen moves.
//
// A read-ahead issued behind a frame that is already on screen runs off the
// clock, the way it runs off the event loop in the program: nobody waits for
// it.
func notchLatency(b *testing.B, m *Model, up bool) time.Duration {
	b.Helper()
	delta := 1
	if up {
		delta = -1
	}
	was := m.preview
	start := time.Now()
	cmd := m.scrollFocus(delta)
	if m.preview != was {
		m.frame()
		took := time.Since(start)
		drain(m, cmd)
		return took
	}
	if cmd == nil {
		return 0
	}
	m.frame()
	drain(m, cmd)
	m.frame()
	return time.Since(start)
}

// drain runs a command and every command its reply produces, the way the
// event loop would.
func drain(m *Model, cmd tea.Cmd) {
	for i := 0; cmd != nil && i < 4; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		updated, follow := m.Update(msg)
		*m = *updated.(*Model)
		cmd = follow
	}
}

func reportScroll(b *testing.B, samples []time.Duration, forks, notches int) {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	pct := func(p float64) float64 {
		if len(samples) == 0 {
			return 0
		}
		i := int(float64(len(samples)-1) * p)
		return float64(samples[i].Microseconds()) / 1000
	}
	b.ReportMetric(pct(0.5), "p50ms/notch")
	b.ReportMetric(pct(0.9), "p90ms/notch")
	b.ReportMetric(float64(forks)/float64(notches), "forks/notch")
	if load, err := os.ReadFile("/proc/loadavg"); err == nil {
		b.Logf("loadavg %s", strings.TrimSpace(string(load)))
	}
	b.Logf("notches=%d forks=%d p50=%.2fms p90=%.2fms", notches, forks, pct(0.5), pct(0.9))
}

// benchScrollBurst spins the wheel through deep history the way a person
// does: a long run of notches in one direction, then back down.
func benchScrollBurst(b *testing.B, m *Model, forks *forkCounter) {
	const burst = 60
	b.ResetTimer()
	var samples []time.Duration
	before := forks.count(b)
	notches := 0
	for i := 0; i < b.N; i++ {
		for j := 0; j < burst; j++ {
			if d := notchLatency(b, m, true); d > 0 {
				samples = append(samples, d)
			}
			notches++
		}
		for j := 0; j < burst; j++ {
			if d := notchLatency(b, m, false); d > 0 {
				samples = append(samples, d)
			}
			notches++
		}
	}
	b.StopTimer()
	reportScroll(b, samples, forks.count(b)-before, notches)
}

// BenchmarkNativeTmuxCopyModeNotch is the bar the operator named: one wheel
// notch in tmux's own copy mode. A real client's wheel is a mouse event down
// the server socket and a repaint back, so a scroll-up driven over the
// control socket is the same server-side work, timed the same way. What it
// leaves out is the client's own repaint, which for the manager is the
// Bubble Tea frame.
func BenchmarkNativeTmuxCopyModeNotch(b *testing.B) {
	m, sessID := focusedWithHistory(b, "benchnative")
	deepen(b, m, sessID)
	// A control client of the manager's own anchor session, which is the
	// only kind there is now: OpenControl took a session to attach to and
	// that is what let a client into a session the manager did not create.
	control, err := m.tmux.OpenPollControl(m.tmux.TargetFor(sessID).Socket)
	if err != nil {
		b.Skipf("no control client: %v", err)
	}
	defer control.Close()
	target := tmux.SessionName(sessID)
	if _, err := control.Command("copy-mode -u -t " + target); err != nil {
		b.Fatalf("copy-mode: %v", err)
	}
	var samples []time.Duration
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 60; j++ {
			start := time.Now()
			if _, err := control.Command("send-keys -X -N " +
				strconv.Itoa(focusScrollStep) + " -t " + target + " scroll-up"); err != nil {
				b.Fatalf("scroll-up: %v", err)
			}
			samples = append(samples, time.Since(start))
		}
		if _, err := control.Command("send-keys -X -t " + target + " history-bottom"); err != nil {
			b.Fatalf("history-bottom: %v", err)
		}
	}
	b.StopTimer()
	reportScroll(b, samples, 0, len(samples))
}

func BenchmarkFocusScrollManaged(b *testing.B) {
	forks := newForkCounter(b)
	m, sessID := focusedWithHistory(b, "benchmanaged")
	deepen(b, m, sessID)
	benchScrollBurst(b, m, forks)
}

func BenchmarkFocusScrollAdopted(b *testing.B) {
	forks := newForkCounter(b)
	m := adoptedWithHistory(b)
	benchScrollBurst(b, m, forks)
}

// deepen fills a pane's scrollback so a burst of notches has somewhere to go.
func deepen(b testing.TB, m *Model, sessID string) {
	b.Helper()
	command := `i=1; while [ "$i" -le 2000 ]; do printf 'bench-line-%04d\n' "$i"; i=$((i+1)); done`
	if err := m.tmux.SendText(sessID, command); err != nil {
		b.Fatalf("SendText: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sessID)
		if err != nil {
			b.Fatalf("capture: %v", err)
		}
		if strings.Contains(pane, "bench-line-2000") {
			break
		}
		if time.Now().After(deadline) {
			b.Fatalf("pane never filled: %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitPaneQuiet(b, m, sessID)
	m.pane.history = paneHistorySize(b, m, sessID)
	seedLive(b, m, sessID)
}

// adoptedWithHistory is the case that dominates this machine: a pane the
// manager took over rather than created.
//
// This helper used to wait for a mirror to come up and skip when none did.
// None ever does -- a mirror on an adopted pane is a client inside the
// operator's own tmux session, which is what exhausted their server on
// 2026-08-27 -- so the wait became a permanent skip, and two scrollback
// benchmarks stopped running while still reporting green. Scrollback on an
// adopted pane is a capture-pane read against that pane's own server, over
// the pooled anchor client where there is one and a fork where there is not.
func adoptedWithHistory(b testing.TB) *Model {
	b.Helper()
	m, socket, pane := adoptedFocus(b, "sh")
	sess, ok := m.selected()
	if !ok {
		b.Fatal("no selected session")
	}
	fill := `i=1; while [ "$i" -le 2000 ]; do printf 'bench-line-%04d\n' "$i"; i=$((i+1)); done`
	if err := m.tmux.SendText(sess.ID, fill); err != nil {
		b.Fatalf("SendText: %v", err)
	}
	foreignPaneContains(b, socket, pane, func(s string) bool {
		return strings.Contains(s, "bench-line-2000")
	})
	// Settle the resize focusSelected asked for. A window is pinned to the
	// preview panel only while it is actually being previewed, so a pane
	// still at its own window's size here would be compared against frames
	// measured at the panel's.
	m.resizeNow(b)
	m.pane.history = adoptedHistorySize(b, m, sess.ID)
	seedLive(b, m, sess.ID)
	return m
}

func adoptedHistorySize(b testing.TB, m *Model, sessID string) int {
	b.Helper()
	size, err := m.tmux.PaneState(sessID, "#{history_size}")
	if err != nil {
		b.Fatalf("history size: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(size))
	if err != nil || n == 0 {
		b.Fatalf("adopted pane reports no history: %q", size)
	}
	return n
}
