package ui

import (
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// Spans for the path the operator actually feels.
//
// A poll pass already traces, and a pass's duration is not what anybody is
// waiting on: the board polls on its own clock while the operator's hand is
// on a key. Bubble Tea renders after every message rather than on a clock,
// so the felt number is one key press through its handler and out the far
// side of the frame it caused -- two calls, on either side of the event
// loop, which no single measurement here could reach before.
//
// Everything is guarded at the call site. At sixty frames a second a span is
// real allocation on the path it is measuring, so a board nobody is tracing
// pays one atomic load per message and per frame and nothing else.

// keyPaint is a key press waiting for its repaint. It is carried on the model
// rather than closed over, because the frame that ends it is a separate call
// Bubble Tea makes into whatever model update answered with.
type keyPaint struct {
	name    string
	at      time.Time
	handled time.Time
	// steps is what the handler blocked on, carried across with the press so
	// the frame that closes its span out can hang them underneath it.
	steps []handlerStep
}

// handlerStep is one blocking call a handler made while the event loop was
// its to hold.
//
// A handler's own duration says that a message was slow; it does not say on
// what. The calls that make it slow -- a tmux round trip, a walk of a process
// tree -- record spans of their own, but those land in traces of their own
// and can be matched back to the press that waited on them only by their
// clocks. Collecting them here puts them under it instead, which is the shape
// the question is actually asked in.
type handlerStep struct {
	name  string
	start time.Time
	end   time.Time
	attrs []tracing.Attr
}

// traceStep reports one blocking call made from inside a handler, to be
// emitted as a child of whatever span that handler turns out to own.
//
// It is called at the site that blocks rather than around the dispatch,
// because the point is to separate the wait from the work: the same handler
// is instant when tmux answers at once and seconds long when the server is
// busy, and only a span around the call itself tells those two apart.
func (m *Model) traceStep(name string, started time.Time, attrs ...tracing.Attr) {
	if !tracing.Enabled() {
		return
	}
	ended := time.Now()
	// Not every caller is inside a dispatch. The same calls are made from
	// startup and from paths the board runs on its own, and a step recorded
	// there has no handler to belong to -- so it stands alone rather than
	// waiting on a dispatch that will never come to collect it and being
	// dropped.
	if !m.dispatching {
		tracing.Record(name, started, ended, nil, attrs...)
		return
	}
	m.handlerSteps = append(m.handlerSteps, handlerStep{name: name, start: started, end: ended, attrs: attrs})
}

// takeSteps hands back what this dispatch collected and clears the slot. The
// backing array is dropped rather than kept: a handler that blocks at all is
// the exception, and holding one press's allocation for the life of the board
// to save it is the worse trade.
func (m *Model) takeSteps() []handlerStep {
	steps := m.handlerSteps
	m.handlerSteps = nil
	return steps
}

// addSteps hangs collected steps under a trace's root.
func addSteps(trace *tracing.Trace, steps []handlerStep) {
	for _, step := range steps {
		trace.Child(step.name, step.start, step.end, step.attrs...)
	}
}

// traceDispatch reports one trip through update.
//
// A key press gets no span of its own. What the operator feels is not how
// long the handler ran, it is how long the board took to show the result, so
// the press is parked on the model and the frame closes it out. Every other
// message is work nobody is waiting on with a finger still down, and stands
// alone: a handler that has gone slow is then attributable to its message
// type without having to know which of them was in flight.
//
// Every ui.update carries msg, the key path included, because an attribute a
// span omits is read back as the empty string and groups like any other
// value. A press keyed only by which key it was does not drop out of a
// summary by message type, which would at least be visible -- it silently
// becomes a group with no name on it.
func (m *Model) traceDispatch(model tea.Model, msg tea.Msg, key tea.KeyPressMsg, isKey bool, started time.Time) {
	ended := time.Now()
	m.dispatching = false
	steps := m.takeSteps()
	if !isKey {
		name := msgName(msg)
		if len(steps) == 0 {
			tracing.Record("ui.update", started, ended, nil, tracing.Attr{Key: "msg", Value: name})
			return
		}
		trace := tracing.NewTrace("ui.update", started, ended, tracing.Attr{Key: "msg", Value: name})
		addSteps(trace, steps)
		trace.Emit()
		return
	}
	// Parked on whatever update answered with, for the same reason the log
	// line reads the mode off there: that is the model Bubble Tea will ask
	// for the frame. A press whose handler quits the program is never
	// painted and so is never reported, which is the honest answer -- there
	// was no frame to feel.
	target := m
	if updated, ok := model.(*Model); ok {
		target = updated
	}
	target.keyPaint = keyPaint{name: traceKeyName(key), at: started, handled: ended, steps: steps}
}

// echoBaseline reads what a pane looked like before a key reaches it, and
// reports what that read cost apart from the handler around it.
//
// It is a capture-pane: a fork, and a round trip to a tmux server this board
// shares with every session on it. The event loop is held for the whole of
// it, and on a loaded server it is comfortably the slowest thing any key
// handler does. "A letter took two seconds" and "a letter spent two seconds
// waiting on tmux" are different findings, and only the second names a cause,
// so the wait gets a span of its own.
//
// The clock is read whether or not anybody is tracing, unlike the guarded
// paths above. Those run at frame rate; this runs once per key press that
// arms a chase, and a branch to save one clock read there would cost more to
// read than it saves.
func (m *Model) echoBaseline(sessID string) string {
	started := time.Now()
	baseline, _ := m.tmux.CapturePane(sessID)
	m.traceStep("ui.echoBaseline", started)
	return baseline
}

// sendFocusKey hands one key to the focused pane, timed for the same reason.
//
// This is usually a write down a pipe the manager already holds, and so is
// usually nothing; it forks when there is no pooled pipe, and a fork here
// queues behind every other client of the same server exactly as the
// baseline does.
func (m *Model) sendFocusKey(sessID, command string) error {
	started := time.Now()
	err := m.tmux.SendRawAt(sessID, command)
	m.traceStep("ui.sendKey", started)
	return err
}

// sendFocusPaste hands pasted text to the focused pane, timed for the same
// reason. It goes through the tmux buffer rather than the pipe -- several
// forked calls, not one write -- so if anything on this path is going to be
// felt, it is this.
func (m *Model) sendFocusPaste(sessID, content string) error {
	started := time.Now()
	err := pasteFocused(m.tmux, sessID, content)
	m.traceStep("ui.sendPaste", started)
	return err
}

// traceFrame reports what a frame cost and, when a press is waiting on it,
// closes out the span the operator feels.
//
// It ends where Bubble Tea takes the string back. The renderer diffs and
// flushes on its own goroutine afterwards, and short-circuits outright when
// the frame is unchanged; ending here reports the part this program is
// answerable for rather than guessing at the rest.
func (m *Model) traceFrame(started time.Time, reused bool) {
	ended := time.Now()
	pending := m.keyPaint
	m.keyPaint = keyPaint{}
	attrs := []tracing.Attr{
		{Key: "reused", Value: reused},
		{Key: "sessions", Value: len(m.sessions)},
		{Key: "width", Value: m.width},
		{Key: "height", Value: m.height},
		{Key: "mode", Value: m.mode.String()},
	}
	if pending.name == "" {
		tracing.Record("ui.frame", started, ended, nil, attrs...)
		return
	}
	trace := tracing.NewTrace("ui.key_to_paint", pending.at, ended,
		tracing.Attr{Key: "key", Value: pending.name},
		tracing.Attr{Key: "reused", Value: reused},
		tracing.Attr{Key: "mode", Value: m.mode.String()},
	)
	// The gap between these two is Bubble Tea's own turn of the loop, and it
	// is left as the space between the children rather than filled with a
	// span this side did not measure.
	trace.Child("ui.update", pending.at, pending.handled,
		tracing.Attr{Key: "key", Value: pending.name},
		tracing.Attr{Key: "msg", Value: keyMsgName})
	addSteps(trace, pending.steps)
	trace.Child("ui.frame", started, ended, attrs...)
	trace.Emit()
}

// traceKeyName names a press the way telemetry may keep it: a key with a
// name of its own is named, and a printable one is reported only as what
// kind of character it was.
//
// The board is watched by its operator, and what that operator types into it
// -- a session name, a search, a message to an agent -- is theirs. "Which
// key is slow" is answerable from the categories; nothing here needs the
// keystroke itself, so nothing here keeps it.
func traceKeyName(key tea.KeyPressMsg) string {
	var name string
	switch code := key.Code; {
	case code == tea.KeyExtended:
		// A press carrying several runes: its text is exactly the content
		// this must not record.
		name = "text"
	case unicode.IsLetter(code):
		name = "letter"
	case unicode.IsDigit(code):
		name = "digit"
	case code != tea.KeySpace && unicode.IsGraphic(code):
		name = "punct"
	default:
		name = tea.Key{Code: key.Code}.Keystroke()
	}
	if utf8.RuneCountInString(name) <= 1 {
		// A code the terminal reported that has no name would otherwise
		// arrive here as itself.
		name = "punct"
	}
	if key.Mod == 0 {
		return name
	}
	var out strings.Builder
	for _, mod := range keyMods {
		if key.Mod.Contains(mod.bit) {
			out.WriteString(mod.name)
		}
	}
	out.WriteString(name)
	return out.String()
}

// keyMods is fixed order, so ctrl+alt+x and alt+ctrl+x are one thing in a
// query rather than two.
var keyMods = []struct {
	bit  tea.KeyMod
	name string
}{
	{tea.ModCtrl, "ctrl+"},
	{tea.ModAlt, "alt+"},
	{tea.ModShift, "shift+"},
	{tea.ModMeta, "meta+"},
	{tea.ModHyper, "hyper+"},
	{tea.ModSuper, "super+"},
}

// keyMsgName is what msgName answers for a press. It is computed once at init
// rather than per press, because the press's span is built without a message
// in hand and reflect would otherwise run on the key path. It is the same
// string the
// other messages are keyed by on purpose: a summary by msg then covers every
// message the board handles, and the key attribute breaks the presses down
// further for whoever wants that.
var keyMsgName = msgName(tea.KeyPressMsg{})

// msgName keys a span by its message's type. reflect reads back the name the
// compiler already wrote down, where %T formats a fresh copy of it; these
// arrive tens of times a second and the whole point is not to charge the
// path for watching it.
func msgName(msg tea.Msg) string {
	typ := reflect.TypeOf(msg)
	if typ == nil {
		return "nil"
	}
	return typ.String()
}
