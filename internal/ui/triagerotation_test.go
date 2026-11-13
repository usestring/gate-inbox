package ui

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// keepWarmBody is the cache warden's keep-alive as it reaches the inbox.
const keepWarmBody = "[gate-inbox] Prompt-cache keep-alive, not a task: reply only ok, no tools."

// rotationFleet is the board the regression was reported on: questions that
// have waited for days, and a handful of warm sessions the operator touched
// recently, two of them urgent, each carrying the cache warden's deadline.
func rotationFleet(t *testing.T, questions int) (*Model, []string, []string) {
	t.Helper()
	var fleet []store.Session
	var asks, warm []string
	for i := range questions {
		name := fmt.Sprintf("ask-%02d", i)
		asks = append(asks, name)
		fleet = append(fleet, smartSess(name, status.Waiting, 72*time.Hour-time.Duration(i)*time.Hour))
	}
	for i := range 4 {
		name := fmt.Sprintf("warm-%d", i)
		warm = append(warm, name)
		sess := smartSess(name, status.Finished, time.Duration(i+1)*time.Minute)
		if i < 2 {
			sess.Priority = priority.Urgent
		}
		fleet = append(fleet, sess)
	}
	m := smartFleet(t, fleet...)
	m.queueDeadlines = map[string]time.Time{}
	for _, name := range warm {
		m.queueDeadlines[name] = smartNow.Add(50 * time.Minute)
	}
	m.rebuildRows()
	return m, asks, warm
}

// keepWarm is one cache-warden pass: every warm session answers a keep-alive
// with "ok", which ends in a fresh finish at at.
func keepWarm(m *Model, warm []string, at time.Time) {
	if m.choreRests == nil {
		m.choreRests = map[string]time.Time{}
	}
	for i := range m.sessions {
		if !slices.Contains(warm, m.sessions[i].ID) {
			continue
		}
		m.sessions[i].Status = status.Finished
		m.sessions[i].Acked = false
		m.sessions[i].LastStatusAt = at
		m.choreRests[m.sessions[i].ID] = at
	}
	m.rebuildRows()
}

// handOverAll walks the drain the way the operator does: hand over the head,
// leave it (which mutes it), let the warden pass, and hand over again.
func handOverAll(t *testing.T, m *Model, warm []string, hops int) []string {
	t.Helper()
	var handed []string
	left := ""
	for hop := range hops {
		i, ok := m.nextTriageInput(left, nil)
		if !ok {
			break
		}
		sess := m.rows[i].sess
		handed = append(handed, sess.Name)
		m.mute(sess)
		left = sess.ID
		keepWarm(m, warm, smartNow.Add(time.Duration(hop+1)*time.Second))
	}
	return handed
}

// The reported regression: with twelve sessions stopped on a question and
// four warm sessions kept alive by the cache warden, triage handed over only
// the four warm ones, round and round. Every question must come round, ahead
// of every warm session, and no session may be handed over twice before the
// queue has been walked.
func TestTriageReachesEveryQuestionPastWarmSessions(t *testing.T) {
	const questions = 12
	m, asks, warm := rotationFleet(t, questions)
	handed := handOverAll(t, m, warm, questions+len(warm)+4)
	if len(handed) < questions {
		t.Fatalf("drain handed over %v, want all %d questions", handed, questions)
	}
	if got := handed[:questions]; !sameSet(got, asks) {
		t.Fatalf("first %d handovers = %v, want every question %v", questions, got, asks)
	}
	seen := map[string]bool{}
	for _, name := range handed {
		if seen[name] {
			t.Fatalf("drain handed %q over twice: %v", name, handed)
		}
		seen[name] = true
	}
}

// The rail agrees with the drain: every question sorts above the warm
// sessions, urgent or not, deadline or not.
func TestTriageRailPutsQuestionsAboveWarmSessions(t *testing.T) {
	m, asks, _ := rotationFleet(t, 6)
	got := sessionNames(m)
	if !sameSet(got[:len(asks)], asks) {
		t.Fatalf("rail = %v, want the %d questions first", got, len(asks))
	}
}

// A keep-alive's "ok" is no news: a session whose latest turn was one sinks
// below every real rest, an idle row included, does not count as needing a
// person, and a mute made before it keeps holding.
func TestTriageSinksKeepAliveRests(t *testing.T) {
	m := smartFleet(t,
		smartSess("kept-warm", status.Finished, time.Minute),
		smartSess("done", status.Finished, 3*time.Hour),
		smartSess("napping", status.Idle, 5*time.Hour),
	)
	warm, _ := m.sessionByID("kept-warm")
	m.mute(warm)
	keepWarm(m, []string{"kept-warm"}, smartNow.Add(time.Second))
	assertRail(t, m, "done", "napping", "kept-warm")
	sess, _ := m.sessionByID("kept-warm")
	if m.needsPerson(sess) {
		t.Fatal("a keep-alive finish counts as needing a person")
	}
	if !m.triageWalkable(sess) {
		t.Fatal("a keep-alive rest left the rail's walk; it should still be reachable last")
	}
	if !m.isMuted(sess) {
		t.Fatal("a keep-alive lapsed the mute the operator's visit left")
	}
	if got := drainOrder(m); !slices.Equal(got, []string{"done", "napping"}) {
		t.Fatalf("drain = %v, want the muted keep-alive rest skipped", got)
	}
}

// A mute does not outlast the operator's own turn: work they started after
// muting finished before the keep-alive landed, so the keep-alive is not all
// the session holds.
func TestTriageKeepAliveKeepsNoMuteOverTheOperatorsTurn(t *testing.T) {
	m := smartFleet(t, smartSess("answered", status.Waiting, time.Hour))
	sess, _ := m.sessionByID("answered")
	m.mute(sess)
	m.sessions[0].OperatorAt = smartNow.Add(-30 * time.Minute)
	keepWarm(m, []string{"answered"}, smartNow.Add(-time.Minute))
	sess, _ = m.sessionByID("answered")
	if m.isMuted(sess) {
		t.Fatal("a keep-alive kept a mute standing over the operator's later turn")
	}
}

// The operator's own turn after a keep-alive is news again: the mark lapses
// and the session is a real finish.
func TestTriageKeepAliveRestLapsesOnTheOperatorsTurn(t *testing.T) {
	m := smartFleet(t,
		smartSess("kept-warm", status.Finished, time.Minute),
		smartSess("napping", status.Idle, 5*time.Hour),
	)
	keepWarm(m, []string{"kept-warm"}, smartNow.Add(-30*time.Second))
	assertRail(t, m, "napping", "kept-warm")
	m.sessions[0].OperatorAt = smartNow
	m.choreRests = choreRestsOf(m.sessions, map[string]store.InboxMessage{
		"kept-warm": {SenderID: store.OperatorVoicedSenderID("cache-warden"), Body: keepWarmBody, DeliveredAt: smartNow.Add(-30 * time.Second)},
	})
	m.rebuildRows()
	assertRail(t, m, "kept-warm", "napping")
}

func TestChoreRestsOfReadsOnlyChoresAfterTheOperator(t *testing.T) {
	at := smartNow
	sessions := []store.Session{
		{ID: "chore", OperatorAt: at.Add(-time.Hour)},
		{ID: "stale-chore", OperatorAt: at.Add(time.Minute)},
		{ID: "typed", OperatorAt: at.Add(-time.Hour)},
		{ID: "from-peer", OperatorAt: at.Add(-time.Hour)},
		{ID: "none"},
	}
	got := choreRestsOf(sessions, map[string]store.InboxMessage{
		"chore":       {SenderID: store.OperatorVoicedSenderID("cache-warden"), Body: keepWarmBody, DeliveredAt: at},
		"stale-chore": {SenderID: store.OperatorVoicedSenderID("cache-warden"), Body: keepWarmBody, DeliveredAt: at},
		"typed":       {SenderID: store.HumanSenderID, Body: keepWarmBody, DeliveredAt: at},
		"from-peer":   {SenderID: "some-session", Body: "[gate-inbox] From lead: please rebase", DeliveredAt: at},
	})
	if want := map[string]time.Time{"chore": at}; len(got) != 1 || !got["chore"].Equal(at) {
		t.Fatalf("chore rests = %v, want %v", got, want)
	}
}

// Dead sessions are on the board but never handed over, and never sort above
// a session that can take input.
func TestTriageNeverHandsOverDeadSessions(t *testing.T) {
	gone := smartSess("gone", status.Dead, time.Minute)
	gone.Priority = priority.Urgent
	m := smartFleet(t,
		gone,
		smartSess("also-gone", status.Dead, 2*time.Minute),
		smartSess("ask", status.Waiting, 3*time.Hour),
		smartSess("done", status.Finished, 4*time.Hour),
	)
	if got := drainOrder(m); !slices.Equal(got, []string{"ask", "done"}) {
		t.Fatalf("drain = %v, want only the live sessions", got)
	}
	rail := sessionNames(m)
	if slices.Index(rail, "gone") < slices.Index(rail, "done") {
		t.Fatalf("rail = %v, want dead rows under the live ones", rail)
	}
}

// A hibernated session is resting work that still needs steering, so the
// drain hands it over once everything awake is done -- after errored rows,
// whatever its priority -- in smart triage and the strict order alike. A row
// that really died stays off.
func TestTriageHandsOverHibernatedSessionsLast(t *testing.T) {
	for _, smart := range []bool{true, false} {
		sleeper := smartSess("sleeper", status.Dead, time.Minute)
		sleeper.Priority = priority.Urgent
		m := smartFleet(t,
			sleeper,
			smartSess("gone", status.Dead, 2*time.Minute),
			smartSess("broke", status.Errored, 3*time.Hour),
			smartSess("ask", status.Waiting, 4*time.Hour),
			smartSess("napping", status.Idle, 5*time.Hour),
		)
		m.smartTriage = smart
		m.hibernated = []string{"sleeper"}
		m.rebuildRows()
		if got := drainOrder(m); !slices.Equal(got, []string{"ask", "napping", "broke", "sleeper"}) {
			t.Fatalf("smart=%v: drain = %v, want the hibernated session last and the dead one skipped", smart, got)
		}
		sess, _ := m.sessionByID("sleeper")
		if m.needsPerson(sess) {
			t.Fatalf("smart=%v: a hibernated session counts as needing a person", smart)
		}
	}
}

func sameSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// The reported case: a parent waiting on a child's two-hour watch, kept warm
// by the cache warden so its last turns are a keep-alive and "ok", showed on
// the first triage tab with nothing to answer. That "ok" is a chore rest and
// stays off the walk. A background turn with an answer of its own is a
// finish: it is handed over after the questions, and neither counts as a
// question.
func TestTriageSkipsAKeptWarmDelegatingParentButHandsOverBackground(t *testing.T) {
	parent := smartSess("delegating-parent", status.Delegating, time.Minute)
	parent.Priority = priority.Urgent
	m := smartFleet(t,
		parent,
		smartSess("watching", status.Background, 2*time.Minute),
		smartSess("ask-a", status.Waiting, 5*time.Hour),
		smartSess("ask-b", status.Waiting, 6*time.Hour),
		smartSess("done", status.Finished, 3*time.Hour),
		smartSess("napping", status.Idle, 4*time.Hour),
	)
	m.queueDeadlines = map[string]time.Time{"delegating-parent": smartNow.Add(50 * time.Minute)}
	m.choreRests = choreRestsOf(m.sessions, map[string]store.InboxMessage{
		"delegating-parent": {SenderID: store.OperatorVoicedSenderID("cache-warden"), Body: keepWarmBody, DeliveredAt: smartNow},
	})
	m.rebuildRows()
	if got := drainOrder(m); !slices.Equal(got, []string{"ask-a", "ask-b", "watching", "done", "napping"}) {
		t.Fatalf("drain = %v, want questions, then the finishes, without the kept-warm parent", got)
	}
	for _, name := range []string{"delegating-parent", "watching"} {
		sess, _ := m.sessionByID(name)
		if m.needsPerson(sess) || m.triageWalkable(sess) != (name == "watching") {
			t.Fatalf("%s: needs a person or walkability is wrong", name)
		}
	}
	rail := sessionNames(m)
	if slices.Index(rail, "delegating-parent") < slices.Index(rail, "napping") {
		t.Fatalf("rail = %v, want the kept-warm parent under every ready session", rail)
	}
	m.triagePickupCmd()
	m.triageResume = true
	if cmd := m.triagePickupCmd(); cmd != nil {
		sess := m.rows[m.cursor].sess
		if sess.Name == "delegating-parent" {
			t.Fatalf("a drained queue picked %s back up", sess.Name)
		}
	}
}
