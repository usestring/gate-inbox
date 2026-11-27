package limitepisode

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

const banner = "You've hit your weekly limit · resets Friday at 9pm"

var t0 = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

func session(account string, launched time.Time) store.Session {
	return store.Session{ID: "s1", Tool: "claude", Account: account, CreatedAt: t0.Add(-time.Hour), AgentLaunchedAt: launched}
}

func facts(sess store.Session) Facts {
	return Facts{Session: sess, Running: true, Routed: true, Accounts: true}
}

// A banner new to the session is a turn the limit cut off: it is worth one
// move and is offered on the card once the move is overdue.
func TestANewBannerIsACutTurnTheExtensionMoves(t *testing.T) {
	sess := session("WORK1", t0)
	ep := Observe(nil, sess, banner, t0)
	if ep == nil || ep.Replayed || ep.Account != "WORK1" || !ep.SeenAt.Equal(t0) {
		t.Fatalf("Observe = %+v", ep)
	}
	d := Decide(ep, facts(sess))
	if !d.Limited || !d.Cut || !d.Move || d.Continue || !d.Subscription {
		t.Fatalf("Decide = %+v", d)
	}
	if !d.CardAt.Equal(t0.Add(MoveGrace)) {
		t.Fatalf("CardAt = %v, want the grace after the banner", d.CardAt)
	}
	if Observe(ep, sess, banner, t0.Add(time.Minute)) != nil {
		t.Fatal("the same banner on the same launch is not the same episode")
	}
}

// A session that is not doing anything is never moved or continued in the
// background: a relaunch that only replays its old banner, a muted session,
// and one with no running agent. Each would spend a turn nobody asked for.
func TestAnInactiveSessionIsNeitherMovedNorContinued(t *testing.T) {
	sess := session("WORK1", t0)
	first := Observe(nil, sess, banner, t0)

	revived := session("WORK1", t0.Add(3*time.Hour))
	replay := Observe(first, revived, banner, t0.Add(3*time.Hour))
	if replay == nil || !replay.Replayed {
		t.Fatalf("a relaunch replaying the banner is not marked a replay: %+v", replay)
	}
	if d := Decide(replay, facts(revived)); !d.Limited || d.Cut || d.Move || d.Continue {
		t.Fatalf("replayed banner: Decide = %+v, want limited with nothing done", d)
	}

	muted := sess
	muted.Muted = true
	if d := Decide(first, facts(muted)); d.Move || d.Continue || !d.CardAt.IsZero() {
		t.Fatalf("muted: Decide = %+v", d)
	}
	asleep := facts(sess)
	asleep.Running = false
	if d := Decide(first, asleep); d.Move || d.Continue {
		t.Fatalf("not running: Decide = %+v", d)
	}
	codex := sess
	codex.Tool = "codex"
	codexEp := Observe(nil, codex, "You've hit your usage limit. Try again at 9pm.", t0)
	codexReplay := Observe(codexEp, session("WORK1", t0.Add(time.Hour)), codexEp.Banner, t0.Add(time.Hour))
	replayed := session("WORK1", t0.Add(time.Hour))
	replayed.Tool = "codex"
	if d := Decide(codexReplay, Facts{Session: replayed, Running: true}); d.Continue {
		t.Fatalf("a replayed Codex banner was continued again: %+v", d)
	}
	if d := Decide(codexEp, Facts{Session: codex, Running: true}); !d.Continue {
		t.Fatalf("a cut Codex turn was not continued: %+v", d)
	}
}

// Once the session has been moved off the account that hit the limit, by
// an extension or by hand, or migrated to another session, or archived, the
// episode is settled: no card, no move.
func TestAnActedOnEpisodeIsSettled(t *testing.T) {
	sess := session("WORK1", t0)
	ep := Observe(nil, sess, banner, t0)

	moved := session("WORK2", t0.Add(time.Minute))
	replay := Observe(ep, moved, banner, t0.Add(time.Minute))
	if d := Decide(replay, facts(moved)); d.Limited || !d.CardAt.IsZero() || d.Move {
		t.Fatalf("moved off the account: Decide = %+v", d)
	}

	migrated := facts(sess)
	migrated.Successor = true
	if d := Decide(ep, migrated); d.Limited || !d.CardAt.IsZero() || d.Move {
		t.Fatalf("migrated away: Decide = %+v", d)
	}

	archived := sess
	archived.Archived = true
	if d := Decide(ep, facts(archived)); d.Limited {
		t.Fatalf("archived: Decide = %+v", d)
	}

	offered := *ep
	offered.Offered = true
	if d := Decide(&offered, facts(sess)); !d.CardAt.IsZero() || !d.Move {
		t.Fatalf("offered: Decide = %+v, want no second card and the move still due", d)
	}
}

// Without an extension routing accounts nothing moves the session, so the
// card opens as soon as the limit is seen.
func TestWithoutRoutingTheCardOpensAtOnce(t *testing.T) {
	sess := session("", t0)
	ep := Observe(nil, sess, banner, t0)
	f := facts(sess)
	f.Routed = false
	d := Decide(ep, f)
	if d.Move || !d.CardAt.Equal(t0) || !d.Subscription {
		t.Fatalf("Decide = %+v", d)
	}
}

func TestSuccessorIsALinkedSessionCreatedAfterTheLimit(t *testing.T) {
	source := store.Session{ID: "src", MigrationID: "src", Group: "g"}
	ep := store.LimitRecovery{SeenAt: t0}
	before := store.Session{ID: "old", MigrationID: "src", Group: "g", CreatedAt: t0.Add(-time.Hour)}
	if Successor([]store.Session{source, before}, source, ep) {
		t.Fatal("a session migrated before the limit is no successor of it")
	}
	after := store.Session{ID: "new", MigrationID: "src", Group: "g", CreatedAt: t0.Add(time.Minute)}
	if !Successor([]store.Session{source, before, after}, source, ep) {
		t.Fatal("a migration after the limit was not seen")
	}
	after.Archived = true
	if Successor([]store.Session{source, after}, source, ep) {
		t.Fatal("an archived successor still settles the episode")
	}
}
