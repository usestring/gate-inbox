package ui

import (
	"fmt"
	"testing"
	"time"
)

// What a focused pane costs the machine is captures per second, and until now
// nothing asserted that number. The guards that produce it each have a test --
// the wheel does not buy the keystroke rate, a tick stands down behind a
// chase, an unchanged frame is not repainted -- and every one of them passed
// while the rate they add up to went unmeasured.
//
// That is not a hypothetical gap. focusIntervalStream was introduced to cut a
// measured 83 captures a second to 25, with tests covering the states it keyed
// on; sampling the operator's board afterwards found one pane taking 54 a
// second, because no test ever asked what the states produced together. A
// budget is the assertion that survives someone adding a fourth rate, or a
// condition that lets a fast one through.
//
// The timeline is simulated rather than slept through. The model reads the
// wall clock, so a scenario arms the ages it keys on and steps a virtual clock
// by the interval the model itself asks for next -- which is what tea.Tick
// would have scheduled. A budget test that slept for its window would take a
// window to run and would measure the test machine's load as much as the
// cadence.

// captureRate drives one scenario for window of simulated time and reports the
// captures per second the tick path issued.
//
// arm runs before every tick with the virtual elapsed time, so a scenario says
// what is true of the pane at that moment -- somebody typing, an agent
// printing, nobody there -- rather than setting it once and hoping the real
// clock has not moved underneath.
func captureRate(t testing.TB, m *Model, window time.Duration, arm func(*Model, time.Duration)) float64 {
	return captureRateAfter(t, m, 0, window, arm)
}

// captureRateAfter is captureRate over the part of the window that follows
// settle, for a scenario whose opening burst is legitimate and whose tail is
// the thing under test. Averaging the two together answers neither question:
// one keystroke buys 750ms at the keystroke rate, which is 62 captures, and
// that alone lifts a ten-second average clear of the quiet budget however
// correctly the cadence falls back afterwards.
func captureRateAfter(t testing.TB, m *Model, settle, window time.Duration, arm func(*Model, time.Duration)) float64 {
	t.Helper()
	if settle >= window {
		t.Fatalf("settle %v leaves nothing of the %v window to measure", settle, window)
	}
	var captures int
	for elapsed := time.Duration(0); elapsed < window; {
		arm(m, elapsed)
		interval := m.previewInterval()
		if interval <= 0 {
			t.Fatalf("previewInterval returned %v; a cadence of zero is an unbounded loop", interval)
		}
		before := m.focusCapturing
		updated, _ := m.Update(previewTickMsg{})
		*m = *updated.(*Model)
		if !before && m.focusCapturing && elapsed >= settle {
			captures++
		}
		// The capture returns well inside one interval -- a fork is about
		// three milliseconds against a 12ms floor -- so the next tick finds
		// the path clear. Leaving it set would have the in-flight guard
		// silence the cadence and report a budget every scenario passes.
		m.focusCapturing = false
		elapsed += interval
	}
	return float64(captures) / (window - settle).Seconds()
}

// budgets are the rates the cadences are allowed to produce. They are ceilings
// with headroom rather than exact figures: the point is to catch a rate that
// escapes its tier, not to re-derive the constants.
//
// Each is its interval's rate plus a little, so a scenario that quietly starts
// taking the tier above it fails here. 1000/12ms is 83/s, 1000/40ms is 25/s,
// 1000/250ms is 4/s.
const (
	budgetTyping    = 90.0 // focusIntervalActive, the keystroke latency promise
	budgetStreaming = 30.0 // focusIntervalStream, an agent printing
	budgetQuiet     = 6.0  // focusIntervalIdle, a pane left on screen
)

// A pane nobody has touched is the common case on a board of dozens, and it is
// the one that must be cheap: a session is focused for minutes and typed into
// for seconds.
func TestAQuietFocusedPaneIsCheap(t *testing.T) {
	m, _ := enterFocus(t, "quietbudget")
	rate := captureRate(t, m, 10*time.Second, func(m *Model, _ time.Duration) {
		m.focusActiveAt = time.Time{}
		m.focusRepaintAt = time.Time{}
		m.focusWheelAt = time.Time{}
	})
	t.Logf("quiet focused pane: %.1f captures/sec", rate)
	if rate > budgetQuiet {
		t.Fatalf("quiet focused pane took %.1f captures/sec, over the %.1f budget", rate, budgetQuiet)
	}
}

// An agent printing its output is read, not typed into, so it earns the rate a
// person can follow rather than the one a keystroke needs. This is the
// scenario that regressed: a spinner leaves every frame different, so nothing
// about the pane ever goes quiet on its own.
func TestAStreamingPaneStaysOnTheStreamingRate(t *testing.T) {
	m, _ := enterFocus(t, "streambudget")
	rate := captureRate(t, m, 10*time.Second, func(m *Model, _ time.Duration) {
		m.focusActiveAt = time.Time{}
		m.focusWheelAt = time.Time{}
		m.noteFocusRepaint()
	})
	t.Logf("streaming pane: %.1f captures/sec", rate)
	if rate > budgetStreaming {
		t.Fatalf("streaming pane took %.1f captures/sec, over the %.1f budget; "+
			"it is on the keystroke cadence, which is what focusIntervalStream exists to prevent",
			rate, budgetStreaming)
	}
}

// Typing is the one case that may spend: a character just typed must not wait
// on a clock. The budget still holds it to its own tier rather than letting it
// run free.
func TestTypingBuysTheKeystrokeRateAndNoMore(t *testing.T) {
	m, _ := enterFocus(t, "typingbudget")
	rate := captureRate(t, m, 10*time.Second, func(m *Model, _ time.Duration) {
		m.focusWheelAt = time.Time{}
		m.noteFocusActivity()
	})
	t.Logf("pane being typed into: %.1f captures/sec", rate)
	if rate > budgetTyping {
		t.Fatalf("typed-into pane took %.1f captures/sec, over the %.1f budget", rate, budgetTyping)
	}
	if rate < budgetStreaming {
		t.Fatalf("typed-into pane took only %.1f captures/sec; the keystroke "+
			"cadence is a latency promise and this is slower than streaming", rate)
	}
}

// A pane stops being typed into long before the operator moves on, and the
// cadence has to fall back on its own. A rate that stays high after the last
// keystroke is a board that costs the keystroke price for as long as a session
// is focused, which is the shape of the original 83/s finding.
func TestTheRateFallsBackAfterTheLastKeystroke(t *testing.T) {
	m, _ := enterFocus(t, "falloffbudget")
	// One keystroke at the start, then nobody. The 750ms that keystroke buys
	// is the cadence working as intended, so the tail after it is what says
	// whether the rate fell back.
	rate := captureRateAfter(t, m, 2*time.Second, 10*time.Second, func(m *Model, elapsed time.Duration) {
		m.focusWheelAt = time.Time{}
		m.focusRepaintAt = time.Time{}
		if elapsed == 0 {
			m.noteFocusActivity()
			return
		}
		// Age the keystroke by the virtual elapsed time: the model reads the
		// wall clock, which has barely moved.
		m.focusActiveAt = time.Now().Add(-elapsed)
	})
	t.Logf("one keystroke, then the tail from 2s to 10s: %.1f captures/sec", rate)
	if rate > budgetQuiet {
		t.Fatalf("a pane quiet since 750ms still took %.1f captures/sec eight seconds "+
			"later, over the %.1f quiet budget: the cadence did not fall back", rate, budgetQuiet)
	}
}

// The list preview is deliberately not covered here. In list mode the tick
// issues its capture inside the closure previewCmd returns and sets nothing on
// the model, so the focusCapturing seam this harness counts never moves: a
// budget written against it would pass at any rate, including an unbounded
// one. Counting those needs the fork counter in internal/tmux and a window
// short enough to execute rather than simulate, which is its own change.
// TestAWheelTurnIsNotTyping pins the list's interval selection meanwhile.

// The whole board's bill, which is the number that shows up as load: one
// focused pane at its rate plus a preview for the row under the cursor. It is
// reported rather than asserted tightly, so a change that moves it says so in
// the test log even when every tier is still inside its own budget.
func TestReportTheBoardsCaptureBill(t *testing.T) {
	m, _ := enterFocus(t, "billbudget")
	for _, tc := range []struct {
		name string
		arm  func(*Model, time.Duration)
	}{
		{"quiet", func(m *Model, _ time.Duration) {
			m.focusActiveAt, m.focusRepaintAt, m.focusWheelAt = time.Time{}, time.Time{}, time.Time{}
		}},
		{"streaming", func(m *Model, _ time.Duration) {
			m.focusActiveAt, m.focusWheelAt = time.Time{}, time.Time{}
			m.noteFocusRepaint()
		}},
		{"typing", func(m *Model, _ time.Duration) {
			m.focusWheelAt = time.Time{}
			m.noteFocusActivity()
		}},
	} {
		rate := captureRate(t, m, 5*time.Second, tc.arm)
		// ~3ms a fork, measured by BenchmarkCaptureExecFork.
		fmt.Printf("  focused pane %-10s %6.1f captures/sec  ~%.0f%% of a core\n",
			tc.name, rate, rate*3.0/10.0)
	}
}
