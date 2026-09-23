package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracetest"
)

// Bubble Tea runs Update and View on one goroutine, so every measurement here
// is about the same thing: what the board was doing instead of answering the
// operator. The traces these assert on came from a live board -- an unnamed
// bucket of ui.update spans holding the worst stalls on it, a refresh handler
// forking tmux on the loop, and a tick repainting a frame nobody had changed.

// A key press's span has to say what kind of message it was, like every other
// span does.
//
// It did not, and because an empty attribute groups like any other value the
// presses did not go missing from a summary by message type -- they collected
// in a bucket with no name, which is where the two-second handlers were.
func TestAKeyPressSpanSaysWhatMessageItWas(t *testing.T) {
	m := buildModel(t)
	read := tracetest.Capture(t)
	m.width, m.height = 120, 40

	press := tea.KeyPressMsg{Code: 'j', Text: "j"}
	updated, _ := m.Update(press)
	model := updated.(*Model)
	model.frame()

	spans := read()
	var update tracetest.Span
	for _, span := range tracetest.Named(spans, "ui.update") {
		if span.Attr("key") != nil {
			update = span
		}
	}
	if update.Name == "" {
		t.Fatalf("no ui.update span for the press (recorded: %s)", spanNames(spans))
	}
	if got := update.Attr("msg"); got != "tea.KeyPressMsg" {
		t.Fatalf("the press's ui.update span carries msg=%v, so it groups under no message type at all", got)
	}
}

// A handler that blocks reports what it blocked on, under the press that
// waited for it.
//
// Typing into a focused pane forks a capture-pane for the echo baseline and
// then sends the key, both with the event loop held. On the operator's board
// those forks went over a quarter of a second eighty-eight times an hour with
// a median of 1.4 seconds when they did -- and the only span covering them
// was the handler's own, which says a letter was slow and nothing about why.
func TestAFocusKeyReportsTheTmuxCallsItBlockedOn(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "typed-into", t.TempDir(), "")
	m.selectSessionRow(t, "typed-into")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("enter did not focus the row, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	// A chase already out is the case where the handler skips the baseline on
	// purpose, so the press below is the first one after the pane settles.
	m.focusChasing = false

	read := tracetest.Capture(t)
	press := tea.KeyPressMsg{Code: 'x', Text: "x"}
	after, _ := m.Update(press)
	model := after.(*Model)
	model.frame()
	spans := read()

	root := tracetest.One(t, spans, "ui.key_to_paint")
	for _, name := range []string{"ui.echoBaseline", "ui.sendKey"} {
		step := tracetest.One(t, spans, name)
		if step.Parent != root.Trace && step.Trace != root.Trace {
			t.Fatalf("%s is not in the press's trace, so nothing joins the wait to the key that waited", name)
		}
		if !root.Brackets(step) {
			t.Fatalf("%s runs outside the press it is supposed to be part of", name)
		}
	}
}

// One tmux listing for one rebuild.
//
// buildTree said so in a comment and did not do it: a rebuild builds the tree
// twice to settle the cursor, sortTriageWithChildren asks again inside each
// build, and each ask forked its own tmux list-panes -- a call measured on
// the operator's board at a second or worse forty-eight times an hour.
func TestARebuildTakesOneTmuxListing(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "parent", dir, "")
	createSession(t, m, "kid", dir, "")
	loadStoredRows(t, m)
	var parent, kid store.Session
	for _, sess := range m.sessions {
		switch sess.Name {
		case "parent":
			parent = sess
		case "kid":
			kid = sess
		}
	}
	if err := m.store.PlaceSession(kid.ID, parent.Group, parent.ID); err != nil {
		t.Fatalf("PlaceSession: %v", err)
	}
	if err := m.store.UpdateStatus(kid.ID, status.Waiting); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)
	// Triage is the only view that asks which panes are up, and it asks from
	// two places inside every build.
	m.triage = true
	// A cursor the first build moves is what provokes the second one, which
	// is the case this is about.
	m.railCursorSess = "nothing-under-the-cursor"

	read := tracetest.Capture(t)
	// Through the refresh, because that is the handler the board rebuilds
	// from and the one the live trace measured.
	m.Update(refreshMsg{
		sessions:   m.sessions,
		listedAt:   time.Now(),
		groups:     m.groups,
		groupPaths: m.groupPaths,
	})
	spans := read()

	listings := tracetest.Named(spans, "ui.livePanes")
	if len(listings) != 1 {
		t.Fatalf("one rebuild forked %d tmux listings, want 1", len(listings))
	}
}

// The child sweep asks its questions off the event loop.
//
// It runs on the poll clock, and everything it asks is slow: a correlated
// store query over the single shared connection, then a has-session fork per
// candidate to a tmux server this board shares with every pane on it. Done
// inside the refresh handler, the poll clock decided when the board stopped
// answering.
func TestTheChildSweepDoesNotFileOnTheEventLoop(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "parent", dir, "")
	createSession(t, m, "kid", dir, "")
	loadStoredRows(t, m)
	var parent, kid store.Session
	for _, sess := range m.sessions {
		switch sess.Name {
		case "parent":
			parent = sess
		case "kid":
			kid = sess
		}
	}
	if err := m.store.PlaceSession(kid.ID, parent.Group, parent.ID); err != nil {
		t.Fatalf("PlaceSession: %v", err)
	}
	id, _, err := m.store.Enqueue(store.InboxMessage{
		SessionID: parent.ID, SenderID: kid.ID, SenderName: kid.Name,
		Body: "done", SentAt: time.Now(),
	}, store.DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := m.store.MarkDelivered(id, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if err := m.tmux.Kill(kid.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := m.store.UpdateStatus(kid.ID, status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)

	cmd := armChildSweep(t, m)
	// Nothing may have been written yet: arming is all the event loop does.
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived {
		t.Fatalf("the child was filed before the command ran, so the store query and the tmux forks were on the loop: %+v, %v", row, err)
	}
	answer := cmd()
	msg, ok := answer.(childSweptMsg)
	if !ok {
		t.Fatalf("the sweep answered with %T, want childSweptMsg", answer)
	}
	m.applyChildSweep(msg)
	row, err := m.store.Get(kid.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !row.Archived {
		t.Fatalf("the sweep ran off the loop and filed nothing: %+v", row)
	}
}

// A second sweep does not go out behind the first.
//
// The poll clock is faster than a sweep waiting out a busy tmux server, and
// stacking another set of forks behind the set already stuck is what makes a
// slow server slower.
func TestOnlyOneChildSweepIsOutAtATime(t *testing.T) {
	m := buildModel(t)
	m.childSweeping = false
	if cmd := m.sweepFinishedChildren(); cmd == nil {
		t.Fatal("the first sweep armed no command")
	}
	if cmd := m.sweepFinishedChildren(); cmd != nil {
		t.Fatal("a second sweep went out while the first was still running")
	}
	m.applyChildSweep(childSweptMsg{})
	if cmd := m.sweepFinishedChildren(); cmd == nil {
		t.Fatal("the sweep never came back after the first one landed")
	}
}

// A preview tick changes nothing the frame reads, so it must not provoke a
// repaint of what is already on screen.
//
// The tick's whole job is to send a capture out; the capture's answer paints
// when it lands, as previewMsg. What the tick writes -- the capture flags,
// the pane it went out for, the process clock -- no view reads. Bubble Tea
// renders after every message rather than on a clock, so without saying so
// the tick rendered the identical frame again at the cadence it ticks on,
// which in a focused pane being typed into is once every twelve milliseconds.
func TestAPreviewTickDoesNotRepaintAnIdenticalFrame(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "watched", t.TempDir(), "")
	m.selectSessionRow(t, "watched")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("enter did not focus the row, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	// The tick stands down while a capture or a chase is already out, and
	// those branches have always said the frame was unchanged. The one this
	// is about is the tick that arms a capture of its own.
	m.focusCapturing, m.focusChasing = false, false

	before, _ := m.paint()
	if strings.TrimSpace(before) == "" {
		t.Fatal("the board painted nothing, so this measures nothing")
	}
	if _, ok := m.selected(); !ok {
		t.Fatal("no session is selected, so the tick takes the branch that already stood down")
	}
	ticked, _ := m.Update(previewTickMsg{})
	model := ticked.(*Model)
	after, reused := model.paint()

	if after != before {
		t.Fatalf("a preview tick changed the frame by %d bytes; the fix assumes it changes nothing", len(after)-len(before))
	}
	if !reused {
		t.Fatalf("a preview tick repainted %d identical bytes rather than answering with the frame already on screen", len(after))
	}
}

// The same for the row under the cursor in the list, whose tick also only
// arms a command.
func TestAListPreviewTickDoesNotRepaintAnIdenticalFrame(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "watched", t.TempDir(), "")
	m.selectSessionRow(t, "watched")
	if _, ok := m.selected(); !ok {
		t.Fatal("no session is selected, so the tick takes the branch that already stood down")
	}

	before, _ := m.paint()
	ticked, _ := m.Update(previewTickMsg{})
	model := ticked.(*Model)
	after, reused := model.paint()

	if after != before {
		t.Fatalf("a list preview tick changed the frame by %d bytes", len(after)-len(before))
	}
	if !reused {
		t.Fatalf("a list preview tick repainted %d identical bytes", len(after))
	}
}

func spanNames(spans []tracetest.Span) string {
	out := make([]string, 0, len(spans))
	for _, span := range spans {
		out = append(out, span.Name)
	}
	return strings.Join(out, ", ")
}

// armChildSweep releases whatever an earlier harness step armed and dropped,
// then arms one sweep for the test to run. createSession and its kin drive a
// refresh and throw away the commands it answers with, which on a running
// board Bubble Tea would have run.
func armChildSweep(t *testing.T, m *Model) tea.Cmd {
	t.Helper()
	m.childSweeping = false
	cmd := m.sweepFinishedChildren()
	if cmd == nil {
		t.Fatal("the sweep armed no command")
	}
	return cmd
}
