package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/sysstat"
)

// How a typed character gets back on screen.
//
// The manager does not own the focused pane's terminal; it shows a capture of
// it. So an echo is never "the character was drawn" -- it is "the manager
// looked again, after the pane drew it". Everything here is about looking
// again quickly, and about not looking when nobody is typing.
//
// This replaces a control-mode mirror: a second tmux client attached to the
// watched session, which tmux pushed a %output event to on every paint. It
// was deleted rather than fixed. On 2026-08-27 one such client per focus
// change exhausted the operator's tmux server and destroyed every agent
// running on it, and the guard added afterwards -- refuse adopted panes --
// left 23 of the operator's 25 panes with no mirror and a 300ms timer as
// their only source of frames. Measured on a real pane on this box:
//
//	echo, adopted, 300ms timer (what the operator has)  mean 302ms
//	echo, managed, mirror push                          mean  27ms
//	echo, either, chase over the pooled pipe            mean 0.5-0.75ms
//
// The mirror was never the fast path; its own 25ms paint-settle debounce was
// already forty times slower than simply asking. The 0.5-0.75ms line above is
// history: the chase ran over the pooled client while captures did, and they
// came off it when the server's retention of a control client's replies
// proved to be a leak -- 7.39 kB held per capture, ~33 GiB a day on this
// board. A capture is a fork again, around three milliseconds, which is what
// the budget and pauses below are sized against. See the note above
// CaptureScrollback, and tmux/pipe.go for what still rides the pipe.
const (
	// focusEchoBudgetDefault bounds the chase after a keystroke for a tool
	// that names no budget of its own. A pane that has not repainted within
	// it is not echoing this key -- the agent swallowed it, or is busy --
	// and the tick below is what covers it from there. Nothing waits on this
	// budget: it runs off the event loop.
	//
	// It fits Claude Code, which repaints a typed character in 10-25ms on
	// the development host. A slower TUI names its own `echo_budget` rather than
	// raising this for every pane on the board.
	focusEchoBudgetDefault = 40 * time.Millisecond
	// focusEchoPause is how long the chase sleeps before its second look,
	// and focusEchoPauseMax the longest it will sleep between later ones.
	//
	// It backs off rather than polling flat because the two cases are so far
	// apart. A key the pane echoes repaints within a fraction of a
	// millisecond, and the first or second look catches it. A key the pane
	// discards -- a modifier the agent ignores, a keystroke into a busy
	// process -- never repaints at all, and a flat 250µs poll would spend
	// the whole budget on about a hundred and sixty captures to learn that.
	// Backing off keeps the fast case fast and the futile case cheap.
	focusEchoPause    = 200 * time.Microsecond
	focusEchoPauseMax = 2 * time.Millisecond
)

// The focused pane's own cadence, which is a fallback and a catch-up rather
// than the echo path: it is what shows the agent's own output, and what
// covers a keystroke whose repaint arrived after the chase gave up.
//
// Two rates, because a focused pane is not the same thing as a watched one.
// Somebody typing is looking at every frame; a focused pane nobody has
// touched for a second is a pane left on screen, and sampling it hard is
// spending the operator's CPU on a window they walked away from.
const (
	focusIntervalActive = 12 * time.Millisecond
	// focusIntervalStream is the cadence of a pane repainting on its own with
	// nobody typing into it: an agent writing its output, and a spinner
	// turning under it.
	//
	// It is a third rate because a repaint is not a keystroke. The keystroke
	// rate is a latency promise -- a character just typed must not wait on a
	// clock -- and output nobody typed makes no such promise, while 25 frames
	// a second is already more than a person reads. Holding the two at one
	// rate cost 83 forked captures a second, about 60% of a core measured on
	// the operator's board, for as long as any working agent stayed focused:
	// a turning spinner alone leaves every frame different, so the fast rate
	// never lapsed.
	focusIntervalStream = 40 * time.Millisecond
	focusIntervalIdle   = 250 * time.Millisecond
	// focusActiveFor is how long after the last keystroke, or the last
	// changed frame, the pane counts as being typed into or repainting.
	focusActiveFor = 750 * time.Millisecond
	// focusCaptureStale is how long the tick's capture may be out before
	// another tick takes its own: a fork that never returns must not leave
	// the focused pane frozen on screen. It is focusReadStale's guarantee
	// for the cadence rather than for the wheel.
	focusCaptureStale = time.Second
)

// procInterval is how often the focused view resamples the process tree
// behind the pane. It is deliberately not the capture cadence: a capture is
// one pipe round trip, while process stats walk a tree of /proc and would be
// the whole cost of the fast path if they rode along with it.
const procInterval = 1200 * time.Millisecond

// noteFocusActivity marks the focused pane as one somebody is typing into,
// which is what buys it the keystroke cadence for the next focusActiveFor.
func (m *Model) noteFocusActivity() {
	m.focusActiveAt = time.Now()
}

// releaseFocusActive ends the keystroke tier early, for a key whose echo the
// chase has already brought back.
//
// The fast tick is documented as a fallback and a catch-up: what it covers is
// "a keystroke whose repaint arrived after the chase gave up". A chase that
// saw the echo has already put that frame on screen, so the 750ms of 12ms
// ticks focusActiveFor would otherwise hold open are re-reading a pane that
// has nothing further to say about that key. They are the board's largest
// single cost: a pane typed into once a second sat in the 83/s tier three
// quarters of the time and forked about sixty captures a second, which is
// what a live sample of 54/s turned out to be.
//
// A deaf key keeps the hold, because that is the case the tier was written
// for -- the chase looked for the whole budget, saw nothing, and a repaint
// that arrives late has only the tick to catch it.
//
// Releasing does not drop the pane to the idle rate. setPreview marks a
// changed frame as a repaint, so a pane that just echoed falls to
// focusIntervalStream and keeps being followed at a rate a person reads,
// which is the tier its own output has always earned.
func (m *Model) releaseFocusActive() {
	m.focusActiveAt = time.Time{}
}

// noteFocusRepaint marks the focused pane as one writing its own output. It
// is deliberately not the same mark: what the operator typed has a deadline
// and what the agent printed does not, so a pane repainting by itself earns
// focusIntervalStream rather than the keystroke rate.
func (m *Model) noteFocusRepaint() {
	m.focusRepaintAt = time.Now()
}

// focusActive reports whether the focused pane has been typed into recently
// enough to be worth sampling at the keystroke rate.
func (m *Model) focusActive() bool {
	return !m.focusActiveAt.IsZero() && time.Since(m.focusActiveAt) < focusActiveFor
}

// noteFocusWheel marks the pane on screen as one the wheel is turning. It is
// its own mark because a notch is not a keystroke: what it asks the pane for
// is a repaint the chase already goes looking for, and the tick behind that
// only has to keep up with a person reading. Marking it as typing bought a
// flick the 12ms keystroke cadence for 750ms after every notch -- 83 forked
// captures a second, most of them reading a frame the chase had just
// brought back.
func (m *Model) noteFocusWheel() {
	m.focusWheelAt = time.Now()
}

// focusWheeling reports whether the wheel turned in the pane on screen
// recently enough for its frames to still be worth following.
func (m *Model) focusWheeling() bool {
	return !m.focusWheelAt.IsZero() && time.Since(m.focusWheelAt) < focusActiveFor
}

// focusStreaming reports whether the focused pane has repainted on its own
// recently enough to still be worth following frame by frame.
func (m *Model) focusStreaming() bool {
	return !m.focusRepaintAt.IsZero() && time.Since(m.focusRepaintAt) < focusActiveFor
}

// echoBudget is how long the chase looks at this tool's panes: the tool's
// own `echo_budget` where it sets one, the default otherwise.
func (m *Model) echoBudget(tool string) time.Duration {
	if budget := m.cfg.Tools[tool].EchoBudget.Duration; budget > 0 {
		return budget
	}
	return focusEchoBudgetDefault
}

// focusEchoCmd looks at the focused pane until it has changed, and reports
// the first frame that differs from the one on screen.
//
// It is armed by the keystroke rather than by a clock, which is what makes
// the echo cost nothing when nobody is typing: no timer runs, and a focused
// pane left alone is sampled at focusIntervalIdle and no faster.
//
// "Changed" is the right stop condition rather than "contains what I typed":
// the manager does not know what the pane will do with a key. Enter, ctrl-c,
// an arrow into a completion menu and a plain letter all repaint, and none of
// them puts the key's own character on screen. A key the pane discards
// repaints nothing, and the chase simply runs out its budget having spent a
// few hundred microseconds looking.
//
// Changed against what matters. The baseline is a capture of the pane taken
// immediately before the key went out -- not the frame on screen, which is
// what this used to compare against and which is only the same thing when
// nothing else has touched the pane since it was painted. A resize reflows
// it, a poll pass may have read it at a different size, and either leaves
// the frame on screen differing from the pane for reasons that have nothing
// to do with this keystroke. The chase then returned on its very first look,
// reporting a frame captured before the key as though it were the echo, and
// the typed character did not appear until the next tick. Reading the pane
// itself is a fork on the key path, around three milliseconds, and it is the
// only baseline that means "before this key" -- which is what it buys, not
// speed.
//
// Nothing here is ever painted speculatively. The frame that lands is a real
// capture of the real pane, so the view can never show a character the pane
// did not accept, and a redraw, a readline edit or an autocomplete rewrite
// arrives as itself rather than as a local guess to be reconciled.
func (m *Model) focusEchoCmd(sessID string, gen uint64, was string, budget time.Duration) tea.Cmd {
	driver := m.tmux
	if driver == nil || sessID == "" {
		return nil
	}
	return func() tea.Msg {
		deadline := time.Now().Add(budget)
		pause := focusEchoPause
		for {
			pane, err := driver.CapturePane(sessID)
			if err != nil {
				return nil
			}
			echoed := pane != was
			if echoed || time.Now().After(deadline) {
				return previewMsg{
					sessID: sessID,
					gen:    gen,
					at:     time.Now(),
					chase:  true,
					echoed: echoed,
					// A frame with no caret in it reads as a dead terminal,
					// and the caret is what the operator is watching while
					// they type -- so it is read on the same round trip
					// rather than a cadence behind the text it sits in.
					facts:   readFacts(driver, sessID),
					factsOK: true,
					preview: pane,
				}
			}
			time.Sleep(pause)
			if pause *= 2; pause > focusEchoPauseMax {
				pause = focusEchoPauseMax
			}
		}
	}
}

// readFacts asks tmux where the caret is and what the pane's application has
// claimed. A read that fails leaves the zero facts, which the caller marks
// as unusable rather than storing as "no mouse, no history".
func readFacts(driver paneReader, sessID string) paneFacts {
	var facts paneFacts
	state, err := driver.PaneState(sessID, paneStateFormat)
	if err != nil {
		return facts
	}
	applyPaneState(&facts, state)
	return facts
}

// paneReader is the slice of the driver the focused reads use, so the chase
// can be exercised against a stub without a tmux server.
type paneReader interface {
	PaneState(id, format string) (string, error)
}

// captureGate is what a focused tick knows, before it forks anything, about
// whether the pane it is about to capture can have changed since the last
// look at it.
type captureGate struct {
	// force takes the capture whatever the stamp says. tmux bumps
	// #{window_activity} for pane output and for nothing else, so the three
	// things it cannot see each set this: a keystroke (which the pane may
	// discard, repainting nothing, and which still owes the operator a
	// look), a wheel notch, and a reflow under a size tmux was just told.
	// A pane the manager has not captured before is the same case.
	force bool
	// sinceSec is the unix second the last capture of this pane was taken
	// in, which is what the stamp is measured against.
	sinceSec int64
}

// shouldCapture decides, from tmux's own #{window_activity}, whether the
// pane can have changed since the last capture of it.
//
// The stamp has one-second resolution, and that is the whole difficulty: it
// names the second the pane last wrote in, never where in that second. A
// capture taken at 10.2s has therefore seen only some of what a stamp of 10
// covers -- more output may land at 10.7 without moving the stamp at all.
// So the rule is the conservative one: keep capturing until the clock has
// left the stamped second, which is the first moment the stamp can prove
// everything it covers already happened before the last look.
//
// That costs at most one second of ticks after a pane goes quiet, and buys
// silence for as long as it stays quiet. Reading it the other way -- trust
// an equal stamp and skip -- would drop a real repaint for up to a second,
// which is a frame the operator is sitting in front of waiting for.
func shouldCapture(gate captureGate, facts paneFacts) bool {
	if gate.force || !facts.activityOK {
		return true
	}
	return facts.activity >= gate.sinceSec
}

// focusCaptureCmd is the focused pane's tick capture: the pane and its facts,
// and the process tree only when procInterval has passed. Splitting the two
// is what lets the tick run at focusIntervalActive at all -- sampling a
// process tree eighty times a second would cost more than every capture on
// the board put together.
//
// The gate is read first and inside this goroutine rather than on the event
// loop, because the stamp rides the pooled pipe: asking whether the pane
// moved costs tens of microseconds, and the fork it can call off costs
// around three milliseconds. Reading it here rather than a tick earlier is
// what keeps the wake-up free -- a pane that has just started writing is
// captured on that very tick, not on the one after.
//
// A forced tick does not pay for the stamp. It is going to capture either
// way, and the facts read after the capture are the ones it owes anyway: the
// caret belongs to the text it sits in rather than to the frame before it.
func (m *Model) focusCaptureCmd(sessID string, gen uint64, withProc bool, gate captureGate) tea.Cmd {
	driver := m.tmux
	if driver == nil || sessID == "" {
		return nil
	}
	return func() tea.Msg {
		msg := previewMsg{sessID: sessID, gen: gen, at: time.Now()}
		var facts paneFacts
		capture := gate.force
		if !capture {
			facts = readFacts(driver, sessID)
			capture = shouldCapture(gate, facts)
		}
		if capture {
			pane, err := driver.CapturePane(sessID)
			if err != nil {
				return nil
			}
			msg.preview, facts = pane, readFacts(driver, sessID)
		} else {
			// No frame was taken, so this message carries no capture time:
			// letting it advance previewAt would age a frame nobody
			// replaced and drop a real capture still in flight behind it.
			msg.skipped, msg.at = true, time.Time{}
		}
		msg.facts, msg.factsOK = facts, true
		if withProc {
			if pid, err := driver.PanePID(sessID); err == nil {
				memTotal, _ := sysstat.MemTotalBytes()
				msg.proc = sysstat.Trees([]int{pid})[pid].ScaleToHost(sysstat.LogicalCPUs(), memTotal)
				msg.procOK = true
			}
		}
		return msg
	}
}
