package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// smartNow is the pinned clock every smart triage test reads.
var smartNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// smartSess is a top-level session the operator last gave a turn age before
// smartNow, in st since then.
func smartSess(name, st string, age time.Duration) store.Session {
	at := smartNow.Add(-age)
	return store.Session{ID: name, Name: name, Status: st, LastStatusAt: at, OperatorAt: at}
}

// smartFleet puts sessions on a triage rail ordered by smart triage, on the
// pinned clock.
func smartFleet(t *testing.T, sessions ...store.Session) *Model {
	t.Helper()
	m := buildModel(t)
	m.cfg.Tools = map[string]config.Tool{"claude": {}}
	m.clock = func() time.Time { return smartNow }
	m.smartTriage = true
	for i := range sessions {
		sessions[i].Tool = "claude"
		sessions[i].Cwd = "/tmp"
		sessions[i].CreatedAt = smartNow.Add(-72 * time.Hour)
	}
	m.sessions = sessions
	m.triage = true
	m.rebuildRows()
	return m
}

// drainOrder walks the whole queue the way successive handovers do.
func drainOrder(m *Model) []string {
	var handed []string
	left := ""
	tried := map[string]bool{}
	for {
		i, ok := m.nextTriageInput(left, tried)
		if !ok {
			return handed
		}
		left = m.rows[i].sess.ID
		tried[left] = true
		handed = append(handed, m.rows[i].sess.Name)
	}
}

func assertRail(t *testing.T, m *Model, want ...string) {
	t.Helper()
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("rail = %v, want %v", got, want)
	}
}

func TestSmartTriageDefaultsOn(t *testing.T) {
	m := buildModel(t)
	if !storedSmartTriage(m.store) {
		t.Fatal("smart triage is off with nothing stored")
	}
	for raw, want := range map[string]bool{"off": false, "on": true, "": true} {
		if err := m.store.SetSetting(smartTriageSetting, raw); err != nil {
			t.Fatal(err)
		}
		if got := storedSmartTriage(m.store); got != want {
			t.Fatalf("stored %q = %v, want %v", raw, got, want)
		}
	}
}

func TestSmartTriageSettingsRowTogglesAndSaves(t *testing.T) {
	m := buildModel(t)
	m.smartTriage = true
	m.openSettings()
	m.settings.field = settingsFieldSmartTriage
	m.cycleSetting(1)
	m.persistSettings()
	if m.smartTriage {
		t.Fatal("the settings row did not turn smart triage off")
	}
	if storedSmartTriage(m.store) {
		t.Fatal("smart triage off was not saved")
	}
}

// Two sessions the operator turned to at the same moment queue by state: the
// question first.
func TestSmartTriagePutsAQuestionAboveAFinishOfTheSameTurn(t *testing.T) {
	m := smartFleet(t,
		smartSess("done", status.Finished, time.Minute),
		smartSess("ask", status.Waiting, time.Minute),
	)
	assertRail(t, m, "ask", "done")
}

// The session the operator turned to most recently goes first, whatever its
// state: its context is the one they still hold.
func TestSmartTriageHandsOverTheNewestTurnFirst(t *testing.T) {
	m := smartFleet(t,
		smartSess("ask", status.Waiting, 10*time.Minute),
		smartSess("done", status.Finished, 5*time.Minute),
	)
	assertRail(t, m, "done", "ask")
	m = smartFleet(t,
		smartSess("ask", status.Waiting, 5*time.Minute),
		smartSess("done", status.Finished, 10*time.Minute),
	)
	assertRail(t, m, "ask", "done")
}

// A low session never overtakes a medium one, however recent: the tier is
// the operator's order, not something recency can outrun.
func TestSmartTriageLowNeverOvertakesMedium(t *testing.T) {
	low := smartSess("low", status.Waiting, time.Minute)
	low.Priority = priority.Low
	med := smartSess("med", status.Finished, 24*time.Hour)
	med.Priority = priority.Medium
	assertRail(t, smartFleet(t, low, med), "med", "low")
}

func TestSmartTriagePutsUrgentAtTheHead(t *testing.T) {
	urgent := smartSess("urgent", status.Finished, 10*time.Minute)
	urgent.Priority = priority.Urgent
	m := smartFleet(t,
		smartSess("q-old", status.Waiting, 40*time.Minute),
		smartSess("done-old", status.Finished, time.Hour),
		urgent,
		smartSess("q-fresh", status.Waiting, time.Minute),
	)
	assertRail(t, m, "urgent", "q-fresh", "q-old", "done-old")
}

// Idle is on the walk like everything else, so a session wrongly read as
// idle still comes round; what is never handed over sorts under every
// walkable row.
func TestSmartTriageHandsOverIdle(t *testing.T) {
	m := smartFleet(t,
		smartSess("grinder", status.Working, 5*time.Minute),
		smartSess("calm-fresh", status.Idle, time.Minute),
		smartSess("done", status.Finished, time.Hour),
		smartSess("calm-old", status.Idle, 3*time.Hour),
		smartSess("gone", status.Dead, 4*time.Minute),
	)
	assertRail(t, m, "calm-fresh", "done", "calm-old", "grinder", "gone")
	if got, want := drainOrder(m), []string{"calm-fresh", "done", "calm-old"}; !slices.Equal(got, want) {
		t.Fatalf("drain = %v, want %v and never the working or dead row", got, want)
	}
}

func TestSmartTriageLeavesAMutedSessionLast(t *testing.T) {
	muted := smartSess("muted", status.Waiting, 5*time.Hour)
	muted.Muted = true
	m := smartFleet(t,
		muted,
		smartSess("ask", status.Waiting, time.Minute),
		smartSess("grinder", status.Working, time.Hour),
	)
	assertRail(t, m, "ask", "grinder", "muted")
	if got := drainOrder(m); slices.Contains(got, "muted") {
		t.Fatalf("drain = %v handed over a muted session", got)
	}
}

func TestSmartTriageDrainsInRailOrder(t *testing.T) {
	high := smartSess("high", status.Errored, 20*time.Minute)
	high.Priority = priority.High
	m := smartFleet(t,
		smartSess("q-old", status.Waiting, 40*time.Minute),
		smartSess("done-aged", status.Finished, 90*time.Minute),
		high,
		smartSess("q-fresh", status.Waiting, time.Minute),
		smartSess("calm", status.Idle, 2*time.Hour),
		smartSess("grinder", status.Working, time.Hour),
	)
	want := []string{"high", "q-fresh", "q-old", "done-aged", "calm"}
	assertRail(t, m, append(want, "grinder")...)
	if got := drainOrder(m); !slices.Equal(got, want) {
		t.Fatalf("drain = %v, want the rail's order %v", got, want)
	}
	// A hop from the middle of the queue still hands over its head.
	if i, ok := m.nextTriageInput("calm", nil); !ok || m.rows[i].sess.Name != "high" {
		t.Fatalf("hop from calm landed on row %d, want the head of the queue", i)
	}
}

// Time passing reorders nothing; the operator turning to a session puts it at
// the head.
func TestSmartTriageOnlyAnOperatorTurnMovesARow(t *testing.T) {
	m := smartFleet(t,
		smartSess("done", status.Finished, 30*time.Minute),
		smartSess("ask", status.Waiting, 10*time.Minute),
	)
	assertRail(t, m, "ask", "done")
	m.clock = func() time.Time { return smartNow.Add(5 * time.Hour) }
	m.rebuildRows()
	assertRail(t, m, "ask", "done")
	m.sessions[0].OperatorAt = smartNow.Add(time.Hour)
	m.rebuildRows()
	assertRail(t, m, "done", "ask")
}

// A working parent is lifted to its child's key and queues on the child's
// recency, not its own.
func TestSmartTriageLiftsAParentByItsChildsScore(t *testing.T) {
	kid := smartSess("kid", status.Finished, 25*time.Minute)
	kid.ParentID, kid.SpawnedBy = "lead", "lead"
	m := smartFleet(t,
		smartSess("ask-old", status.Waiting, 30*time.Minute),
		smartSess("lead", status.Working, 3*time.Hour),
		kid,
		smartSess("ask-young", status.Waiting, 20*time.Minute),
	)
	assertRail(t, m, "ask-young", "lead", "kid", "ask-old")
}

// A finish the operator has looked at is acked and idle, and looking is not
// a turn: it keeps its place on its last turn, and on a tie ranks under an
// unseen finish and above a plain idle session, until input moves it on.
func TestSmartTriageKeepsASeenFinishInTheQueue(t *testing.T) {
	seen := smartSess("seen", status.Idle, time.Hour)
	seen.Acked = true
	m := smartFleet(t,
		smartSess("calm", status.Idle, time.Hour),
		seen,
		smartSess("done", status.Finished, time.Hour),
	)
	assertRail(t, m, "done", "seen", "calm")

	// It goes ahead of an unseen finish the operator turned to before it.
	m = smartFleet(t, seen, smartSess("done", status.Finished, 2*time.Hour))
	assertRail(t, m, "seen", "done")

	// Input moves it on: working, it is off the walk.
	seen.Status = status.Working
	m = smartFleet(t, seen, smartSess("calm", status.Idle, time.Minute))
	assertRail(t, m, "calm", "seen")
	if got := drainOrder(m); slices.Contains(got, "seen") {
		t.Fatalf("drain = %v handed over a working session", got)
	}
}

// Skipping a finished session acknowledges it, and the seen finish is still
// scored; the walk must get past it anyway.
func TestSmartTriageMuteOnAFinishOutlastsItsAck(t *testing.T) {
	done := smartSess("done", status.Finished, time.Hour)
	m := smartFleet(t, done, smartSess("calm", status.Idle, time.Minute))
	m.mute(done)
	m.sessions[0].Status, m.sessions[0].Acked = status.Idle, true
	m.rebuildRows()
	if got := drainOrder(m); !slices.Equal(got, []string{"calm"}) {
		t.Fatalf("drain = %v, want the acked finish still muted", got)
	}
}

// Leaving a page in a walk hands its session over. The walk starts from the
// head of the queue, so the session left behind must be muted or the next
// hop hands it straight back.
func TestSmartTriageLeavingAPageMutesItsSession(t *testing.T) {
	m := smartFleet(t,
		smartSess("ask", status.Waiting, time.Minute),
		smartSess("calm", status.Idle, time.Hour),
	)
	m.cursor = 0
	m.leaveHeadPane(true)
	if !m.isMuted(m.sessions[0]) {
		t.Fatal("leaving the page did not mute the session it was over")
	}
	if i, ok := m.nextTriageInput("calm", nil); ok {
		t.Fatalf("the walk past calm handed over %q, want the drain over", m.rows[i].sess.Name)
	}
}

// A deadline still ahead goes before the score in the scored band.
func TestSmartTriageDeadlineGoesBeforeTheScore(t *testing.T) {
	m := smartFleet(t,
		smartSess("ask", status.Waiting, time.Minute),
		smartSess("calm", status.Idle, time.Hour),
	)
	m.queueDeadlines = map[string]time.Time{"calm": smartNow.Add(time.Minute)}
	m.rebuildRows()
	assertRail(t, m, "calm", "ask")
	m.clock = func() time.Time { return smartNow.Add(2 * time.Minute) }
	m.rebuildRows()
	assertRail(t, m, "ask", "calm")
}

// Smart triage off is the strict order exactly: tier first, then state, then
// oldest, with a seen finish ranked as the idle row it reads as.
func TestSmartTriageOffKeepsTheStrictOrder(t *testing.T) {
	urgent := smartSess("urgent-done", status.Finished, time.Minute)
	urgent.Priority = priority.Urgent
	seen := smartSess("seen", status.Idle, 3*time.Hour)
	seen.Acked = true
	kid := smartSess("kid", status.Finished, 50*time.Minute)
	kid.ParentID, kid.SpawnedBy = "lead", "lead"
	fleet := []store.Session{
		smartSess("calm", status.Idle, 5*time.Hour),
		smartSess("done-aged", status.Finished, 2*time.Hour),
		smartSess("broke", status.Errored, 4*time.Hour),
		urgent,
		seen,
		smartSess("q-fresh", status.Waiting, time.Minute),
		smartSess("q-old", status.Waiting, 40*time.Minute),
		smartSess("lead", status.Working, 3*time.Hour),
		kid,
	}
	m := smartFleet(t, fleet...)
	m.smartTriage = false
	m.rebuildRows()
	want := []string{"urgent-done", "q-old", "q-fresh", "done-aged", "lead", "kid", "calm", "seen", "broke"}
	assertRail(t, m, want...)
	if got, want := drainOrder(m), slices.DeleteFunc(slices.Clone(want), func(n string) bool { return n == "lead" || n == "kid" }); !slices.Equal(got, want) {
		t.Fatalf("drain = %v, want %v", got, want)
	}
}

// Recency counts from the operator's last turn, not from the last status
// change. A turn Gate Inbox started -- a queued message, a name sweep, a
// nudge -- ends in a fresh finish, and a session nobody has looked at in
// hours must not jump the queue because of it.
func TestSmartTriageAGateInboxTurnDoesNotMoveARowUp(t *testing.T) {
	stale := smartSess("stale", status.Finished, 3*time.Hour)
	stale.LastStatusAt = smartNow.Add(-time.Minute)
	m := smartFleet(t,
		smartSess("fresh", status.Finished, time.Hour),
		stale,
	)
	assertRail(t, m, "fresh", "stale")
	if got, want := triageScore(stale), triageRecency(smartNow.Add(-3*time.Hour)); got != want {
		t.Fatalf("score = %v, want %v from the operator's turn", got, want)
	}
}

// A session nobody has given a turn since it was made queues on its
// creation.
func TestSmartTriageQueuesOnCreationWithNoOperatorTurn(t *testing.T) {
	untouched := smartSess("untouched", status.Finished, time.Minute)
	untouched.OperatorAt = time.Time{}
	m := smartFleet(t, smartSess("touched", status.Finished, time.Hour), untouched)
	// smartFleet makes every session 72 hours before smartNow.
	assertRail(t, m, "touched", "untouched")
}

// Messages Gate Inbox, another session or an extension queued start turns
// without moving the operator's clock; the operator's own line from a shell
// moves it.
func TestSmartTriageOnlyAnOperatorMessageMovesTheWaitClock(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	deliverAll := func() {
		t.Helper()
		for attempt := 0; attempt < 20; attempt++ {
			if queued, _ := m.store.QueuedCount(sess.ID); queued == 0 {
				return
			}
			if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
				t.Fatalf("maybeDeliverInbox: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("a message was never delivered")
	}

	before := time.Now()
	queueFrom(t, m, sess.ID, store.SystemSenderID, "cache warm")
	queueFrom(t, m, sess.ID, "sender01", "from another agent")
	queueFrom(t, m, sess.ID, store.ExtensionSenderID("watcher"), "from an extension")
	queueFrom(t, m, sess.ID, store.OperatorVoicedSenderID("watcher"), "in the operator's voice")
	queueFrom(t, m, sess.ID, store.RelayedHumanSenderID, "relayed by a helper")
	deliverAll()
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastStatusAt.After(before) {
		t.Fatalf("delivery left LastStatusAt at %v; the test needs the turns to have started", got.LastStatusAt)
	}
	if !got.OperatorAt.IsZero() {
		t.Fatalf("a message nobody typed stamped the operator's turn at %v", got.OperatorAt)
	}

	queueFrom(t, m, sess.ID, store.HumanSenderID, "yes, ship it")
	deliverAll()
	if got, err = m.store.Get(sess.ID); err != nil {
		t.Fatal(err)
	}
	if got.OperatorAt.Before(before) {
		t.Fatalf("the operator's own line left their turn at %v", got.OperatorAt)
	}
}

// A line or a key the board saw land is the operator's turn.
func TestSmartTriageABoardSendMovesTheWaitClock(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	before := time.Now()
	m.noteOperator(sess, extension.OperatorPrompt, "go on", false)
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OperatorAt.Before(before) {
		t.Fatalf("a landed prompt left the operator's turn at %v", got.OperatorAt)
	}
}

// An attached pane's keys never pass through the board. A turn that started
// while the operator was in it is theirs; going in and out again is a look,
// which leaves the wait alone like looking at a finish from the board does.
func TestSmartTriageAnAttachCountsOnlyWhenItGaveATurn(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	m.noteAttachedTurn(sess.ID, time.Now().Add(time.Second))
	if got, err := m.store.Get(sess.ID); err != nil || !got.OperatorAt.IsZero() {
		t.Fatalf("a look stamped the operator's turn: %v, %v", got.OperatorAt, err)
	}

	since := time.Now().Add(-time.Second)
	if err := m.store.UpdateStatus(sess.ID, status.Working); err != nil {
		t.Fatal(err)
	}
	m.noteAttachedTurn(sess.ID, since)
	if got, err := m.store.Get(sess.ID); err != nil || got.OperatorAt.Before(since) {
		t.Fatalf("a turn given while attached left the operator's turn at %v, %v", got.OperatorAt, err)
	}
}

func TestTriageWaitsForStatusDetectionAfterOperatorInput(t *testing.T) {
	for _, state := range []string{status.Finished, status.Idle, status.Waiting} {
		t.Run(state, func(t *testing.T) {
			m := smartFleet(t, smartSess("recent", state, 0), smartSess("next", status.Waiting, time.Minute))
			assertRail(t, m, "recent", "next")
			for _, elapsed := range []time.Duration{0, 15*time.Second - time.Nanosecond, 15 * time.Second} {
				m.clock = func() time.Time { return smartNow.Add(elapsed) }
				i, ok := m.nextTriageInput("", nil)
				want := "next"
				if elapsed >= 15*time.Second {
					want = "recent"
				}
				if !ok || m.rows[i].sess.ID != want {
					t.Fatalf("after %v: candidate %d, found %v; want %s", elapsed, i, ok, want)
				}
			}
			for i := range m.sessions {
				if m.sessions[i].ID == "recent" {
					m.sessions[i].Status = status.Working
				}
			}
			m.rebuildRows()
			i, ok := m.nextTriageInput("", nil)
			if !ok || m.rows[i].sess.ID != "next" {
				t.Fatal("the session reentered triage after detection marked it working")
			}
		})
	}
}
