package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/netwatch"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// goOffline gives the poller a watcher that has seen the network fail often
// enough to call it an outage, and returns a switch that brings it back.
func goOffline(t *testing.T, m *Model) (restore func()) {
	t.Helper()
	up := false
	w := netwatch.New(func(context.Context) bool { return up })
	for i := 0; i < netwatch.FailuresToOffline; i++ {
		w.Check(context.Background(), time.Now())
	}
	if offline, _ := w.Offline(); !offline {
		t.Fatal("the watcher did not go offline")
	}
	m.poller.net = w
	return func() {
		up = true
		w.Check(context.Background(), time.Now())
	}
}

func TestAnOfflinePassHoldsTheMessageUntilTheNetworkReturns(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	id := queueMessage(t, m, sess.ID, "rebase on main")
	restore := goOffline(t, m)

	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
		t.Fatalf("offline pass: %v", err)
	}
	state, err := m.store.Message(id, "sender01")
	if err != nil {
		t.Fatal(err)
	}
	if !state.ClaimedAt.IsZero() || !state.DroppedAt.IsZero() || !state.DeliveredAt.IsZero() {
		t.Fatalf("an offline pass touched the message: claimed %v dropped %v delivered %v",
			state.ClaimedAt, state.DroppedAt, state.DeliveredAt)
	}

	restore()
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
		t.Fatalf("online pass: %v", err)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 0 {
		t.Fatal("the held message was not sent once the network returned")
	}
}

func TestAnOfflineSnippetGoesToTheOutboxAsTheOperatorsWords(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	goOffline(t, m)

	m.sendSentence(sess, "ship it", `"ship it"`, true, false)

	heads := queuedHeads(t, m)
	head, ok := heads[sess.ID]
	if !ok {
		t.Fatalf("nothing queued; error bar: %q", m.errBar.text)
	}
	if head.SenderID != snippetSenderID || head.Body != "ship it" {
		t.Fatalf("queued %q from %q, want the operator's line", head.Body, head.SenderID)
	}
	if capture, _ := m.tmux.CapturePane(sess.ID); strings.Contains(capture, "ship it") {
		t.Fatal("an offline snippet was typed into the pane")
	}
}

func TestHeldLimitContinuationsOnlyIncludesDueUnattemptedCurrentLaunch(t *testing.T) {
	now := time.Now()
	launch := now.Add(-time.Hour)
	for _, tc := range []struct {
		name                           string
		resetAt, attemptedAt, launchAt time.Time
		tool, state                    string
		archived, want                 bool
	}{
		{"due", now.Add(-limitResetGrace), time.Time{}, launch, "codex", status.Errored, false, true},
		{"grace", now.Add(-limitResetGrace + time.Nanosecond), time.Time{}, launch, "codex", status.Errored, false, false},
		{"attempted", now.Add(-time.Hour), now, launch, "codex", status.Errored, false, false},
		{"old launch", now.Add(-time.Hour), time.Time{}, launch.Add(-time.Hour), "codex", status.Errored, false, false},
		{"claude", now.Add(-time.Hour), time.Time{}, launch, "claude", status.Errored, false, false},
		{"working", now.Add(-time.Hour), time.Time{}, launch, "codex", status.Working, false, false},
		{"archived", now.Add(-time.Hour), time.Time{}, launch, "codex", status.Errored, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := store.Session{ID: "limited", Tool: tc.tool, Status: tc.state, CreatedAt: launch, Archived: tc.archived}
			recoveries := map[string]store.LimitRecovery{sess.ID: {ResetAt: tc.resetAt, AttemptedAt: tc.attemptedAt, LaunchAt: tc.launchAt}}
			held := heldLimitContinuations([]store.Session{sess}, recoveries, now)
			if held[sess.ID] != tc.want {
				t.Fatalf("held = %v, want %v", held[sess.ID], tc.want)
			}
		})
	}
}

func TestOfflineLimitRecoveryAppearsPendingUntilRestored(t *testing.T) {
	m := buildModel(t)
	configureLimitCodex(t, m)
	tool := m.cfg.Tools["codex"]
	pane := "You've hit your usage limit. Try again at Sep 5th, 2026 9:00 PM."
	tool.Command = "sh -c " + tmux.ShellQuote("printf '%s\n' "+tmux.ShellQuote(pane)+"; printf '› '; cat"+soleWriter)
	m.cfg.Tools["codex"] = tool
	sess := store.Session{ID: newID(), Name: "limited-offline", Tool: "codex", Cwd: t.TempDir(), Status: status.Errored}
	if err := m.launchNewSession(sess, tool, tool.Command); err != nil {
		t.Fatal(err)
	}
	settledPane(t, m, sess.ID, "You've hit your usage limit")
	restore := goOffline(t, m)
	msg := m.poller.refreshOnce()
	refresh, ok := msg.(refreshMsg)
	if !ok {
		t.Fatalf("refresh returned %T: %v", msg, msg)
	}
	found := false
	for _, shown := range refresh.sessions {
		if shown.ID == sess.ID {
			found = true
			if shown.Status != status.Pending {
				t.Fatalf("offline recovery status = %q, want pending", shown.Status)
			}
		}
	}
	if !found {
		t.Fatal("limited session absent from refresh")
	}
	states, err := m.store.LimitRecoveries()
	if err != nil {
		t.Fatal(err)
	}
	state, ok := states[sess.ID]
	if !ok || !state.AttemptedAt.IsZero() {
		t.Fatalf("offline recovery was not held unattempted: %+v", state)
	}
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status == status.Pending {
		t.Fatal("pending overlay was persisted")
	}
	restore()
	if !recoverLimit(t, m.poller, stored, pane+"\n\n› ", status.Errored, true, true, time.Now()) {
		t.Fatal("held recovery did not resume after connectivity returned")
	}
	settledPane(t, m, sess.ID, "Continue the interrupted task")
}

func TestHeldSnippetPreservesHandoverUntilDelivery(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		handOver, cancel, drop, poll bool
	}{
		{name: "auto proceed", handOver: true},
		{name: "poll confirms landing", handOver: true, poll: true},
		{name: "stay here"},
		{name: "operator kept typing", handOver: true, cancel: true},
		{name: "dropped", handOver: true, drop: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := buildModel(t)
			liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "next": status.Waiting})
			m.triage = true
			m.rebuildRows()
			m.enterFocusOn(t, "ask")
			sess := sessionNamed(t, m, "ask")
			sess.Tool = "claude-hooked"
			observer := &recordingObserver{}
			m.ObserveBoard(observer)
			restore := goOffline(t, m)
			m.sendSentence(sess, "ship it", `"ship it"`, true, tc.handOver)
			head := queuedHeads(t, m)[sess.ID]
			pending := m.heldSnippets[head.ID]
			if pending == nil {
				t.Fatalf("no held snippet: %s", m.errBar.text)
			}
			pending.deadline = time.Now().Add(-time.Hour)
			m.resumeHeldSnippets()
			if len(m.landings) != 0 || focusedName(t, m) != "ask" {
				t.Fatal("a queued snippet started its landing check or moved focus")
			}
			if tc.cancel {
				m.keepLandingHere()
			}
			if tc.drop {
				if err := m.store.MarkDropped(head.ID, time.Now()); err != nil {
					t.Fatal(err)
				}
			} else {
				restore()
				if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
					t.Fatal(err)
				}
			}
			if got := operatorInputs(observer); len(got) != 0 {
				t.Fatalf("snippet reported at delivery instead of landing: %+v", got)
			}
			refreshed := append([]store.Session(nil), m.sessions...)
			if tc.poll {
				for i := range refreshed {
					if refreshed[i].ID == sess.ID {
						refreshed[i].Status = status.Working
					}
				}
			}
			m.Update(refreshMsg{sessions: refreshed, listedAt: time.Now()})
			if len(m.heldSnippets) != 0 {
				t.Fatal("settled message still held")
			}
			if tc.drop {
				if len(m.landings) != 0 || focusedName(t, m) != "ask" {
					t.Fatal("a dropped snippet started a landing or handed over")
				}
				return
			}
			if !tc.poll && (m.landings[sess.ID] != pending || !pending.deadline.After(time.Now())) {
				t.Fatal("delivery did not resume the landing with a fresh window")
			}
			if got := operatorInputs(observer); !tc.poll && len(got) != 0 {
				t.Fatalf("snippet reported before landing: %+v", got)
			}
			if got := inboxEnvelope(head, "", true, messageContext{}); got != "ship it" {
				t.Fatalf("snippet delivered as %q, want the operator's words", got)
			}
			if !tc.poll {
				landAnswer(t, m, sess.ID)
			}
			got := operatorInputs(observer)
			if len(got) != 1 {
				t.Fatalf("operator input reported %d times, want once", len(got))
			}
			if got[0].Via != extension.OperatorSnippet || got[0].MessageID != 0 ||
				got[0].SessionID != sess.ID || got[0].Text != "ship it" || got[0].Dialog || got[0].At.IsZero() {
				t.Fatalf("snippet provenance lost: %+v", got[0])
			}
			want := "ask"
			if tc.handOver && !tc.cancel {
				want = "next"
			}
			if got := focusedName(t, m); got != want {
				t.Fatalf("after delivery and landing, focused %q, want %q", got, want)
			}
		})
	}
}

func TestHeldSnippetRestoresAfterRestartAndForwarding(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		restart, forward, cancel bool
	}{
		{name: "restart", restart: true},
		{name: "forward", forward: true},
		{name: "restart and forward", restart: true, forward: true},
		{name: "cancel survives restart", restart: true, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := buildModel(t)
			sess := spawnedSession(t, m, "claude-hooked")
			observer := &recordingObserver{}
			m.ObserveBoard(observer)
			restore := goOffline(t, m)
			m.sendSentence(sess, "ship it", `"ship it"`, true, true)
			head := queuedHeads(t, m)[sess.ID]
			oldProbe := m.heldSnippets[head.ID].probe
			if tc.cancel {
				m.keepLandingHere()
			}
			if tc.restart {
				m.heldSnippets = nil
				m.heldSnippetsRestored = false
				m.poller.snippetRecipients.Clear()
			}
			recipient := sess
			if tc.forward {
				spawnedSession(t, m, "claude-hooked")
				for _, row := range m.sessionRows() {
					if row.ID != sess.ID {
						var err error
						recipient, err = m.store.Get(row.ID)
						if err != nil {
							t.Fatal(err)
						}
						break
					}
				}
				if recipient.ID == sess.ID {
					t.Fatal("replacement was not created")
				}
				if _, err := m.store.ForwardInbox(sess.ID, recipient.ID); err != nil {
					t.Fatal(err)
				}
			}
			restore()
			if tc.restart || tc.forward {
				if err := deliverInbox(t, m, recipient, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
					t.Fatal(err)
				}
				msg, err := m.store.Message(head.ID, snippetSenderID)
				if err != nil || !msg.DeliveredAt.IsZero() {
					t.Fatalf("delivered before recipient baseline: %+v, %v", msg, err)
				}
			}
			m.resumeHeldSnippets()
			pending := m.heldSnippets[head.ID]
			if pending == nil || pending.sess.ID != recipient.ID || pending.probe == oldProbe && (tc.restart || tc.forward) {
				t.Fatal("held landing not rebuilt for current recipient")
			}
			if pending.handOver == tc.cancel {
				t.Fatalf("handover = %v, cancelled = %v", pending.handOver, tc.cancel)
			}
			if err := deliverInbox(t, m, recipient, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
				t.Fatal(err)
			}
			if got := operatorInputs(observer); len(got) != 0 {
				t.Fatalf("event before landing: %+v", got)
			}
			m.resumeHeldSnippets()
			if m.landings[recipient.ID] == nil {
				t.Fatal("delivered snippet has no landing")
			}
			landAnswer(t, m, recipient.ID)
			got := operatorInputs(observer)
			if len(got) != 1 || got[0].SessionID != recipient.ID || got[0].Via != extension.OperatorSnippet || got[0].Text != "ship it" {
				t.Fatalf("restored snippet event: %+v", got)
			}
			held, err := m.store.HeldSnippets()
			if err != nil || len(held) != 0 {
				t.Fatalf("settled snippet still durable: %+v, %v", held, err)
			}
		})
	}
}
