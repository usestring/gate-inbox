// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// productionEchoWait is the window the driver gives a pane to draw a paste,
// pinned in internal/tmux by TestPasteWindowDefaultsToTheProductionEchoWait.
// Named rather than imported because it is unexported there, and the point
// of these tests is the wall clock a pass sees, not the constant.
const productionEchoWait = time.Second

// handoffBudget is what the pass itself may spend on a delivery. It has to
// sit well under productionEchoWait for the assertion to mean anything: the
// pass still does real work -- a capture, a caret read, two store writes and
// the paste -- and that is tens of milliseconds, so this is loose enough to
// survive a loaded box and still four times inside the window it must not be
// paying.
const handoffBudget = 400 * time.Millisecond

// The measured bug. A pane that does not draw a paste promptly costs the
// driver a full second before it dares press Enter, and the poll pass used
// to spend that second itself: derive.inbox was measured on a live board at
// 1.044s, 1.046s, 1.065s, 1.065s and 1.275s, five passes clustered inside
// twenty milliseconds of the cap. A pass that long leaves every row on the
// board describing a board seconds old and holds up everything else it owes.
//
// Both halves are asserted, and both are needed. The pass must return in
// well under the window -- and the window must still be spent, off the pass,
// or this fixture is not the deaf pane it claims to be and the test would
// hold with the wait deleted outright.
func TestThePassDoesNotSpendTheEchoWindowDeliveringAMessage(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "deaf-tool")
	queueMessage(t, m, sess.ID, "rebase on main")

	start := time.Now()
	if err := m.poller.maybeDeliverInbox(sess, queuedHeads(t, m), tmux.Capture{Text: "❯ "}, status.Idle, true); err != nil {
		t.Fatalf("maybeDeliverInbox: %v", err)
	}
	handoff := time.Since(start)
	if handoff > handoffBudget {
		t.Fatalf("the pass spent %v handing one message to a pane, over the %v budget: it is "+
			"waiting out the %v echo window inside the pass again", handoff, handoffBudget, productionEchoWait)
	}
	if !m.poller.awaitSends(10 * time.Second) {
		t.Fatal("the send handed off by the pass never finished")
	}
	waited := time.Since(start)
	t.Logf("pass spent %v; the send took %v in total", handoff, waited)
	if waited < productionEchoWait-100*time.Millisecond {
		t.Fatalf("the whole delivery took %v, less than the %v window: this pane drew the paste, "+
			"so it never cost the window and this test would pass with the wait deleted",
			waited, productionEchoWait)
	}
	if queued, err := m.store.QueuedCount(sess.ID); err != nil || queued != 0 {
		t.Fatalf("queued = %d (err %v): the message the pass handed off was never delivered", queued, err)
	}
}

// The other three quarters of the win: several sends in one pass. Passes
// carrying more than one were measured at 4.8s and 10.5s, and the fix is
// only worth anything if no delivery in the pile pays a window of its own.
//
// So each delivery is timed on its own and the typical one is held under
// half a window. A pass that waits per pane cannot get under that however
// fast the box is: this fixture never draws a paste, so every such delivery
// costs at least the whole window, and load only adds to it. What load does
// add to is the real work each delivery still does on the pass -- a handful
// of forked tmux calls and two store writes -- which is tens of milliseconds
// idle and was a few hundred under a loaded fleet. A budget on the total
// charged that work five times against a single-delivery allowance, and
// failed on load alone. Comparing against a one-delivery pass would not do
// instead: that work is per pane too, so a healthy pile already costs five
// times one delivery, and so does a regressed one.
//
// The median rather than the slowest, because one delivery descheduled for
// a moment is the box, while a per-pane wait puts every one of them past
// the window.
func TestSeveralDeliveriesInOnePassStayOffIt(t *testing.T) {
	m := buildModel(t)
	const sessions = 5
	var rows []store.Session
	for i := 0; i < sessions; i++ {
		if err := m.spawnSession("deaf-tool", fmt.Sprintf("worker%d", i), t.TempDir(), "", "", false); err != nil {
			t.Fatalf("spawn: %v", err)
		}
	}
	for _, row := range m.sessionRows() {
		sess, err := m.store.Get(row.ID)
		if err != nil {
			t.Fatal(err)
		}
		queueMessage(t, m, sess.ID, "rebase "+sess.Name+" on main")
		rows = append(rows, sess)
	}
	if len(rows) != sessions {
		t.Fatalf("spawned %d rows, want %d", len(rows), sessions)
	}
	heads := queuedHeads(t, m)

	costs := make([]time.Duration, 0, sessions)
	for _, sess := range rows {
		start := time.Now()
		if err := m.poller.maybeDeliverInbox(sess, heads, tmux.Capture{Text: "❯ "}, status.Idle, true); err != nil {
			t.Fatalf("maybeDeliverInbox %s: %v", sess.Name, err)
		}
		costs = append(costs, time.Since(start))
	}
	t.Logf("%d deliveries cost the pass %v", sessions, costs)
	sorted := slices.Clone(costs)
	slices.Sort(sorted)
	if typical := sorted[len(sorted)/2]; typical >= productionEchoWait/2 {
		t.Fatalf("the typical delivery cost the pass %v of a %d-delivery pass (%v), not under half the %v "+
			"window: the pass is paying a window per pane again", typical, sessions, costs, productionEchoWait)
	}
	if !m.poller.awaitSends(20 * time.Second) {
		t.Fatal("the sends handed off by the pass never finished")
	}
	for _, sess := range rows {
		if queued, err := m.store.QueuedCount(sess.ID); err != nil || queued != 0 {
			t.Fatalf("%s: queued = %d (err %v), the message was never delivered", sess.Name, queued, err)
		}
	}
}

// A claim with no delivery recorded is how the manager spots work a previous
// run died in the middle of, and after inboxClaimGrace it retires that
// message rather than risk typing it twice. A paste still out is the one
// thing that looks identical from the store and is not that: retiring it
// would tell the sender the message was dropped while the send still in
// flight went on to deliver it -- the message counted both ways at once.
func TestAMessageThisManagerIsStillPastingIsNotRetiredAsAbandoned(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	id := queueMessage(t, m, sess.ID, "rebase on main")

	// Claimed longer ago than the grace allows, which on its own is the
	// abandoned-claim case.
	if claimed, err := m.store.ClaimMessage(id, time.Now().Add(-inboxClaimGrace-time.Minute)); err != nil || !claimed {
		t.Fatalf("ClaimMessage: %v (claimed %v)", err, claimed)
	}
	// And this manager's own paste out for it.
	if !m.poller.reserveSend(messageSend(id)) {
		t.Fatal("no send slot")
	}
	defer m.poller.releaseSend(messageSend(id))

	if err := m.poller.maybeDeliverInbox(sess, queuedHeads(t, m), tmux.Capture{Text: "❯ "}, status.Idle, true); err != nil {
		t.Fatalf("a message this manager is still pasting was reported dropped: %v", err)
	}
	state, err := m.store.Message(id, "sender01")
	if err != nil {
		t.Fatal(err)
	}
	if !state.DroppedAt.IsZero() {
		t.Fatal("a message whose paste is still in flight was retired as an abandoned claim: " +
			"its sender is told it was dropped and the send delivers it anyway")
	}
}

// The cap has to be a reason not to claim, never a reason to strand
// something already claimed: a pass that finds every slot taken must leave
// the message queued, untouched, for the next one.
func TestAPassWithNoSendSlotLeavesTheMessageQueued(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	id := queueMessage(t, m, sess.ID, "rebase on main")

	for i := 0; i < sendsInFlight; i++ {
		if !m.poller.reserveSend(inputSend(fmt.Sprintf("filler%d", i))) {
			t.Fatalf("slot %d refused before the cap", i)
		}
	}
	if err := m.poller.maybeDeliverInbox(sess, queuedHeads(t, m), tmux.Capture{Text: "❯ "}, status.Idle, true); err != nil {
		t.Fatalf("maybeDeliverInbox: %v", err)
	}
	state, err := m.store.Message(id, "sender01")
	if err != nil {
		t.Fatal(err)
	}
	if !state.ClaimedAt.IsZero() || !state.DroppedAt.IsZero() || !state.DeliveredAt.IsZero() {
		t.Fatalf("a pass with no slot left touched the message anyway: claimed %v dropped %v delivered %v",
			state.ClaimedAt, state.DroppedAt, state.DeliveredAt)
	}
	for i := 0; i < sendsInFlight; i++ {
		m.poller.releaseSend(inputSend(fmt.Sprintf("filler%d", i)))
	}
	// And the next pass, with the slots back, delivers it.
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 0 {
		t.Fatal("the message the capped pass left queued was never delivered by a later one")
	}
}

// The failure the send carries now that it runs off the pass: what the pane
// cost has to reach the operator, and the message has to be recorded dropped
// so its sender is not told it arrived.
func TestASendThatFailsOffThePassIsStillRecordedAndSurfaced(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	id := queueMessage(t, m, sess.ID, "rebase on main")
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true)
	if err == nil {
		t.Fatal("a send into a pane that is gone reported nothing: the operator never learns the message did not land")
	}
	if !strings.Contains(err.Error(), "dropped a message") {
		t.Fatalf("send failure surfaced as %q, want the dropped-message report", err)
	}
	state, err2 := m.store.Message(id, "sender01")
	if err2 != nil {
		t.Fatal(err2)
	}
	if state.DroppedAt.IsZero() {
		t.Fatal("a send that failed left the message neither delivered nor dropped: its sender is still waiting on it")
	}
}

// The pending-input twin of the abandoned-claim rule. A claimed input with
// no delivery recorded is how the manager spots a launch prompt a previous
// run died in the middle of, and it retires that rather than risk running
// the same slash command twice. A paste still out looks exactly like it from
// the store and is not it: reconciling it would report the prompt skipped
// while the send still in flight typed it anyway.
func TestAPendingInputThisManagerIsStillPastingIsNotReconciledAsAmbiguous(t *testing.T) {
	m := buildModel(t)
	if err := m.spawnSession("send-tool", "custom", t.TempDir(), "", "do not resend", false); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	row := m.sessionRows()[0]
	input := sessionPendingInputs(t, m, row.ID)[0]
	if claimed, err := m.store.ClaimPendingInput(row.ID, input); err != nil || !claimed {
		t.Fatalf("claim pending input = %v, %v", claimed, err)
	}
	sess, err := m.store.Get(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !m.poller.reserveSend(inputSend(sess.ID)) {
		t.Fatal("no send slot")
	}
	defer m.poller.releaseSend(inputSend(sess.ID))

	sent, err := m.poller.maybeSendPendingInput(sess, "❯ ", true)
	if err != nil {
		t.Fatalf("an input this manager is still pasting was reconciled away: %v", err)
	}
	if sent {
		t.Fatal("an input still being pasted was reported delivered a second time")
	}
	if inputs := sessionPendingInputs(t, m, sess.ID); len(inputs) != 1 {
		t.Fatalf("the input the send is still carrying was taken off the queue under it: %q", inputs)
	}
}
