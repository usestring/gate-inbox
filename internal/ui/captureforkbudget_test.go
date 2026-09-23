package ui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// The focused pane has two capture paths, and capturebudget_test.go can see
// only one of them.
//
// That harness counts a capture by watching focusCapturing flip across
// Update, which is a seam the tick sets and the chase does not touch: it
// reports the cadence the tick asks for and nothing about the looking the
// echo path does between ticks. Its streaming figure of 25.0/s is exactly
// focusIntervalStream, which is the giveaway -- a number that lands on its
// constant to a tenth is measuring the constant, not the pane.
//
// So the two paths were never added up, and the operator's board was found
// at 54 captures a second against a modelled 25. Everything here counts the
// forks instead. tmux.ExecCounts sits at the driver's exec paths, below
// every seam the UI has, so a capture the chase takes, a capture the tick
// takes and a capture nobody predicted all land in the same counter.
//
// The chase turned out not to be where the 54 came from --
// TestOneKeystrokeASecondHoldsTheKeystrokeTier below is where that answer
// lives, and what pins it.
//
// These run in real time rather than over a simulated timeline. That is the
// price of counting the chase at all: the chase sleeps against the wall
// clock inside a command the event loop never re-enters, so there is no tick
// to step and nothing to virtualise. Windows are seconds, and every budget
// is a ceiling with headroom so a loaded box reports slow rather than
// flaky.

// These seven tests are the package's fixed floor: ~37s of real seconds that no
// hardware shortens, measured at 15% CPU because almost all of it is the wall
// clock passing. That is most of internal/ui's cost on a quiet box and none of
// its coverage of the UI itself, so a run that wants a signal rather than a
// fork audit skips them.
//
// The windows themselves are deliberately left alone. Every budget below is a
// ceiling, but three are also floors -- a rate that drops too far fails just as
// one that climbs too high does -- so a shorter window buys a few seconds by
// making the rate noisier in both directions, on a box whose load has swung
// between 1 and 138 in a day. That trades a fixed cost for a flake, in the
// package that can least afford another one.
func skipUnderShort(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("counts tmux forks over multi-second real-time windows")
	}
}

// focusForkRate runs the model's real event loop for window and reports the
// tmux processes it forked, per subcommand, per second.
//
// It is bubbletea's own loop in miniature -- every command on its own
// goroutine, every message fed back through one Update -- because the thing
// under test only exists when those run concurrently. A harness that ran
// commands inline would serialise the chase behind the tick and measure a
// cadence neither path actually keeps.
//
// keyEvery drives input at a fixed rate where a scenario needs somebody
// typing; zero leaves the pane to itself.
func focusForkRate(t testing.TB, m *Model, window, keyEvery time.Duration, key func() tea.Msg) map[string]float64 {
	t.Helper()
	// Buffered and dropped on overflow: a wedged loop must fail as a rate
	// rather than deadlock the test binary against a full channel.
	msgs := make(chan tea.Msg, 4096)
	var exec func(tea.Cmd)
	exec = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		go func() {
			switch msg := cmd().(type) {
			case nil:
			case tea.BatchMsg:
				for _, sub := range msg {
					exec(sub)
				}
			default:
				select {
				case msgs <- msg:
				default:
				}
			}
		}()
	}
	var keyC <-chan time.Time
	if keyEvery > 0 {
		keys := time.NewTicker(keyEvery)
		defer keys.Stop()
		keyC = keys.C
	}
	intervals := map[time.Duration]int{}
	// The scenario before this one left commands in flight -- a tick sleeping
	// out its interval, a chase sleeping out its budget -- and each of them
	// still forks when it wakes. They are counted against whichever window is
	// open when they land, so the counters are zeroed after the longest of
	// them has had time to finish rather than before.
	time.Sleep(focusIntervalIdle + focusEchoBudgetDefault)
	tmux.ResetExecCounts()
	start := time.Now()
	exec(m.previewTick())
	done := time.After(window)
	for {
		select {
		case <-done:
			elapsed := time.Since(start).Seconds()
			rates := map[string]float64{"TOTAL": float64(tmux.ExecTotal()) / elapsed}
			for verb, n := range tmux.ExecCounts() {
				if n > 0 {
					rates[verb] = float64(n) / elapsed
				}
			}
			// The cadence mix rides back in the same map under a prefix no
			// tmux verb can collide with. These are counts, not rates: what
			// a caller asks of them is which tier the ticks were in, and a
			// tier that never fired must be absent rather than zero.
			for interval, n := range intervals {
				rates["ticks@"+interval.String()] = float64(n)
			}
			t.Logf("  tick cadences over the window: %v", intervals)
			return rates
		case <-keyC:
			updated, cmd := m.Update(key())
			*m = *updated.(*Model)
			exec(cmd)
		case msg := <-msgs:
			if _, tick := msg.(previewTickMsg); tick {
				intervals[m.previewInterval()]++
			}
			updated, cmd := m.Update(msg)
			*m = *updated.(*Model)
			exec(cmd)
		}
	}
}

// typist is a scenario's operator: one printable key each time it is asked,
// cycling so no two keys in a row are the same. A repeated character would
// leave some panes with an unchanged frame and quietly turn a typing
// scenario into a test of the unchanged-frame path.
func typist() func() tea.Msg {
	var n int
	return func() tea.Msg {
		n++
		c := rune('a' + n%26)
		return tea.KeyPressMsg{Code: c, Text: string(c)}
	}
}

// The rates the focused pane is allowed to fork at, counting both paths.
// They are ceilings with headroom over what the tiers measure at, so a
// scenario that escapes its tier fails and a slow afternoon does not.
const (
	// A pane left on screen: focusIntervalIdle's 4/s, plus the process tree
	// at procInterval.
	forkBudgetQuiet = 9.0
	// An agent printing: focusIntervalStream's 25/s. Measured at 21, because
	// a capture that takes a fork's worth of time cannot quite keep a 40ms
	// beat.
	forkBudgetStreaming = 32.0
	// Somebody typing: focusIntervalActive's 83/s, plus whatever the chase
	// takes that the tick did not already stand down for.
	//
	// Measured between 140 and 152 against a pane that never echoes, which
	// is the first number here the tick-only harness could not have produced
	// at all: it is well past the 83/s the keystroke cadence reaches on its
	// own, and the difference is the chase. The ceiling carries more headroom
	// than the others because the chase's share of it moves with how long a
	// fork takes, which is the box's business rather than the cadence's.
	forkBudgetTyping = 190.0
	// What one chase may spend on a single key. A pane that never echoes
	// runs the chase to its full focusEchoBudgetDefault, which is the
	// expensive case and the one this bounds; a baseline capture on the key
	// path is counted with it, because the keystroke pays for both.
	//
	// Measured between 9.3 and 10.7 -- it rises on an idle box, where a
	// quicker fork fits more looks inside the same budget.
	//
	// This is a ceiling on focusEchoBudgetDefault rather than on the backoff,
	// which is worth being explicit about because the two are not equally
	// visible here. What bounds a chase is that budget divided by the cost of
	// one look, and a look is mostly its fork rather than its pause: dropping
	// the backoff to a flat focusEchoPause only reaches about twelve, inside
	// the spread this has to tolerate, while lengthening the budget to 200ms
	// reaches forty-two. A chase that stopped backing off would have to be
	// caught by the typing rate instead.
	forkBudgetPerKeystroke = 13.0
	// A pane typed into once a second, which is the shape the live 54/s
	// sample turned out to be. It measures at 25-30 now that the echo
	// releases the keystroke hold; it measured 60 before.
	forkBudgetSlowTyping = 40.0
)

// A focused pane nobody is touching is most of a board most of the time, and
// it has to cost almost nothing.
func TestAQuietFocusedPaneForksAtItsIdleRate(t *testing.T) {
	skipUnderShort(t)
	m, _ := enterFocus(t, "quietforks")
	// Focusing itself notes activity -- sitting down at a pane buys the
	// keystroke cadence -- and this scenario is the pane a minute later.
	m.focusActiveAt, m.focusRepaintAt, m.focusWheelAt = time.Time{}, time.Time{}, time.Time{}
	rates := focusForkRate(t, m, 3*time.Second, 0, nil)
	t.Logf("quiet focused pane: %.1f tmux forks/sec %v", rates["TOTAL"], rates)
	if rates["TOTAL"] > forkBudgetQuiet {
		t.Fatalf("a focused pane nobody touched forked %.1f tmux processes/sec, over the %.1f budget",
			rates["TOTAL"], forkBudgetQuiet)
	}
}

// An agent printing its output is read, not typed into. This is the tier
// focusIntervalStream exists to create, and the one the tick-only harness
// reports at exactly its own constant -- so it is worth asserting against
// forks, where the chase would show up if it were running here.
func TestAStreamingPaneForksAtItsStreamingRate(t *testing.T) {
	skipUnderShort(t)
	m, _, _ := adoptedFocus(t, "sh -c 'while :; do date +%s%N; sleep 0.04; done'")
	// Let the pane get going, so the window measures a stream rather than
	// the first frame of one.
	time.Sleep(500 * time.Millisecond)
	m.focusActiveAt, m.focusWheelAt = time.Time{}, time.Time{}
	rates := focusForkRate(t, m, 3*time.Second, 0, nil)
	t.Logf("streaming pane: %.1f tmux forks/sec %v", rates["TOTAL"], rates)
	if rates["TOTAL"] > forkBudgetStreaming {
		t.Fatalf("a pane printing its own output forked %.1f tmux processes/sec, over the %.1f "+
			"budget: it is on the keystroke cadence, which is what focusIntervalStream exists to prevent",
			rates["TOTAL"], forkBudgetStreaming)
	}
}

// Typing is the tier that may spend, and the budget is what stops it
// spending without limit. Both paths are live here: the tick at
// focusIntervalActive, and a chase for every key that does not land while
// one is already out.
//
// Against a pane that never echoes, which is where the two paths actually
// compete. A pane that repaints at once ends its chase in a look or two and
// the tick has the window almost to itself; a deaf one leaves a chase out
// for the whole focusEchoBudgetDefault, so what this measures is the tick
// and the chase sharing a pane rather than the tick alone.
func TestTypingForksNoFasterThanTheKeystrokeTier(t *testing.T) {
	skipUnderShort(t)
	m, _, _ := adoptedFocus(t, "sh -c 'stty -echo; sleep 600'")
	time.Sleep(400 * time.Millisecond)
	rates := focusForkRate(t, m, 3*time.Second, 100*time.Millisecond, typist())
	t.Logf("pane typed into at 10 keys/sec: %.1f tmux forks/sec %v", rates["TOTAL"], rates)
	if rates["TOTAL"] > forkBudgetTyping {
		t.Fatalf("a pane typed into at 10 keys/sec forked %.1f tmux processes/sec, over the %.1f "+
			"budget: focusIntervalActive and the chase share this pane, and "+
			"between them they are asking tmux for more than the keystroke cadence alone can",
			rates["TOTAL"], forkBudgetTyping)
	}
}

// The finding the tick-only harness could not reach, pinned so it cannot be
// lost again.
//
// A focused pane sampled at 54 captures a second was read as a cadence bug
// hiding between the tiers. It was not: focusActiveFor held the keystroke
// cadence open for 750ms after every key, so a pane typed into once a second
// sat in the 83/s tier three quarters of the time and forked about sixty
// captures a second. Nothing was leaking; the rate was what the rules asked
// for.
//
// It is no longer what they ask for. A chase that sees the echo releases the
// hold, because the frame behind that key is already on screen and the fast
// tick is documented as the catch-up for a key the chase MISSED. So a slowly
// typed pane now spends the gap between keys on the streaming tier it earned
// by repainting, rather than on the keystroke tier it bought and stopped
// using.
//
// This replaces a floor that asserted the old number. That floor was evidence
// about where the board's cost came from and it said a change dropping it
// should have to say so: this is the change, and 60.7/s -> 29.3/s measured
// like-for-like on one box is what it says.
func TestOneKeystrokeASecondFallsBackOnceTheEchoLands(t *testing.T) {
	skipUnderShort(t)
	m, _ := enterFocus(t, "oneakeyforks")
	rates := focusForkRate(t, m, 4*time.Second, time.Second, typist())
	t.Logf("pane typed into once a second: %.1f tmux forks/sec %v", rates["TOTAL"], rates)
	if rates["TOTAL"] > forkBudgetSlowTyping {
		t.Fatalf("a pane typed into once a second forked %.1f tmux processes/sec, over the %.1f "+
			"budget: the keystroke tier is being held across the gap between keys again, which "+
			"is what made a slowly-typed pane cost 54/s", rates["TOTAL"], forkBudgetSlowTyping)
	}
	if rates["TOTAL"] <= forkBudgetQuiet {
		t.Fatalf("a pane typed into once a second forked only %.1f tmux processes/sec, at or below "+
			"the %.1f a pane nobody touched costs: frames are not reaching the operator while "+
			"they type", rates["TOTAL"], forkBudgetQuiet)
	}
	// The release is the mechanism, so assert it rather than only its rate: a
	// slowly typed pane must spend more ticks on the streaming tier than on
	// the keystroke tier. A rate alone could fall for some other reason.
	if fast, slow := rates["ticks@12ms"], rates["ticks@40ms"]; fast >= slow {
		t.Fatalf("%.0f ticks at the 12ms keystroke cadence against %.0f at 40ms: the echo is not "+
			"releasing the hold, so the pane is still paying the keystroke rate between keys",
			fast, slow)
	}
}

// The case the keystroke tier exists for, which the release must not take
// away. A pane that never echoes gives the chase nothing to see, so it gives
// the release nothing to fire on, and the fast tick stays -- it is the only
// thing left that would catch a repaint arriving after the chase gave up.
//
// Without this, releasing on echo would look correct while having quietly
// removed the fallback from the one pane that depends on it.
func TestADeafKeyKeepsTheKeystrokeTier(t *testing.T) {
	skipUnderShort(t)
	// stty -echo makes the pane deaf: the tty's line discipline no longer
	// echoes the key, so no chase in this window can ever see one.
	m, _, _ := adoptedFocus(t, "sh -c 'stty -echo; sleep 600'")
	time.Sleep(400 * time.Millisecond)
	rates := focusForkRate(t, m, 4*time.Second, time.Second, typist())
	t.Logf("deaf pane typed into once a second: %.1f tmux forks/sec %v", rates["TOTAL"], rates)
	if fast := rates["ticks@12ms"]; fast < rates["ticks@40ms"] {
		t.Fatalf("a deaf pane spent %.0f ticks on the keystroke tier against %.0f on streaming: "+
			"the release fired for a key whose echo was never seen, and a late repaint now has "+
			"nothing to catch it", fast, rates["ticks@40ms"])
	}
	if rates["TOTAL"] < forkBudgetStreaming {
		t.Fatalf("a deaf pane typed into once a second forked only %.1f/sec, at or below the %.1f "+
			"streaming tier: it has lost the keystroke cadence it is owed",
			rates["TOTAL"], forkBudgetStreaming)
	}
}

// What a single key costs on the chase path, which is the half of the bill
// capturebudget_test.go cannot see at all.
//
// Measured against a pane that never echoes, because that is the expensive
// case: a key the pane repaints for is caught in a look or two, and a key it
// swallows runs the chase out to focusEchoBudgetDefault. That budget is what
// this holds, and it is worth holding per key rather than only as a rate --
// a keystroke is the unit an operator produces, and a chase that grew from
// nine forks to forty would be invisible in a rate measured while the tick
// was standing down for it.
func TestAChaseIsBoundedPerKeystroke(t *testing.T) {
	skipUnderShort(t)
	// stty -echo is what makes the pane deaf. Without it the tty's own line
	// discipline echoes every key straight back, the chase's first look
	// already differs, and the test measures the cheap case while claiming
	// the expensive one.
	m, _, _ := adoptedFocus(t, "sh -c 'stty -echo; sleep 600'")
	time.Sleep(400 * time.Millisecond)
	const keys = 6
	var forks int64
	for i := 0; i < keys; i++ {
		// One chase at a time is its own rule with its own test; this
		// measures what one chase costs, so each key starts with the path
		// clear rather than riding the last key's chase.
		m.focusChasing, m.echoPending = false, false
		tmux.ResetExecCounts()
		c := rune('a' + i)
		updated, cmd := m.Update(tea.KeyPressMsg{Code: c, Text: string(c)})
		*m = *updated.(*Model)
		runCmd(t, m, cmd)
		forks += tmux.ExecCounts()["capture-pane"]
	}
	per := float64(forks) / keys
	t.Logf("a key the pane never echoes: %.1f capture-pane forks, baseline included", per)
	if per > forkBudgetPerKeystroke {
		t.Fatalf("one keystroke cost %.1f capture-pane forks, over the %.1f budget: "+
			"focusEchoBudgetDefault is buying more looks at a pane that is not going to answer",
			per, forkBudgetPerKeystroke)
	}
	if per < 2 {
		t.Fatalf("one keystroke cost %.1f capture-pane forks, which is fewer than the baseline "+
			"plus one look: the chase is not running and this budget measures nothing", per)
	}
}

// The focused pane's whole bill, reported rather than asserted, so a change
// that moves it shows up in the numbers even while every tier is still inside
// its own ceiling. The tick-only figures are in capturebudget_test.go; the
// difference between the two is the chase.
//
// A benchmark rather than a test because it asserts nothing and its four
// real-clock windows cost 14 seconds of every run's quiet phase. Run it with
//
//	go test ./internal/ui -run '^$' -bench '^BenchmarkFocusedPaneForkBill$' -benchtime=1x
func BenchmarkFocusedPaneForkBill(b *testing.B) {
	for i := 0; b.Loop(); i++ {
		report := func(name, unit string, m *Model, keyEvery time.Duration, key func() tea.Msg) {
			rates := focusForkRate(b, m, 3*time.Second, keyEvery, key)
			// ~3ms a fork, measured by BenchmarkCaptureExecFork.
			fmt.Printf("  focused pane %-16s %6.1f tmux forks/sec  ~%.0f%% of a core\n",
				name, rates["TOTAL"], rates["TOTAL"]*3.0/10.0)
			b.ReportMetric(rates["TOTAL"], unit)
		}
		quiet, _ := enterFocus(b, fmt.Sprintf("billquietforks%d", i))
		quiet.focusActiveAt, quiet.focusRepaintAt, quiet.focusWheelAt = time.Time{}, time.Time{}, time.Time{}
		report("quiet", "quiet-forks/s", quiet, 0, nil)

		streaming, _, _ := adoptedFocus(b, "sh -c 'while :; do date +%s%N; sleep 0.04; done'")
		time.Sleep(500 * time.Millisecond)
		streaming.focusActiveAt, streaming.focusWheelAt = time.Time{}, time.Time{}
		report("streaming", "streaming-forks/s", streaming, 0, nil)

		slow, _ := enterFocus(b, fmt.Sprintf("billslowforks%d", i))
		report("1 key/sec", "typing1hz-forks/s", slow, time.Second, typist())

		fast, _ := enterFocus(b, fmt.Sprintf("billfastforks%d", i))
		report("10 keys/sec", "typing10hz-forks/s", fast, 100*time.Millisecond, typist())
	}
}
