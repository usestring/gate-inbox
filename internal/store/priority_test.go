package store

import (
	"errors"
	"testing"

	"github.com/usestring/gate-inbox/internal/priority"
)

func groupTiers(t *testing.T, st *Store) map[string]priority.Tier {
	t.Helper()
	groups, err := st.Groups()
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	tiers := map[string]priority.Tier{}
	for _, g := range groups {
		if g.Priority != priority.Unset {
			tiers[g.Name] = g.Priority
		}
	}
	return tiers
}

// A session's own tier round-trips through the store, and a group's tier
// reaches every session below it -- through as many levels as there are --
// without being written onto the sessions, so moving one out of the group
// sheds it.
func TestPriorityRoundTripsAndGroupsCoverTheirSubtree(t *testing.T) {
	st := newTestStore(t)
	st.CreateGroup("proj", "")
	st.CreateGroup("proj/sub", "")
	st.CreateGroup("other", "")
	st.CreateSession(sample("a", "proj"))
	st.CreateSession(sample("b", "proj/sub"))
	st.CreateSession(sample("c", "other"))

	if err := st.SetPriority("c", priority.Urgent); err != nil {
		t.Fatalf("set priority: %v", err)
	}
	if got, _ := st.Get("c"); got.Priority != priority.Urgent {
		t.Fatalf("session tier did not round-trip: %q", got.Priority)
	}
	if err := st.SetGroupPriority("proj", priority.High); err != nil {
		t.Fatalf("set group priority: %v", err)
	}
	tiers := groupTiers(t, st)
	if tiers["proj"] != priority.High || len(tiers) != 1 {
		t.Fatalf("group tiers = %v, want proj alone at high", tiers)
	}
	for _, id := range []string{"a", "b"} {
		sess, _ := st.Get(id)
		if sess.Priority != priority.Unset {
			t.Fatalf("%s carries the group's tier on its own row", id)
		}
		if got := EffectiveTier(tiers, sess); got != priority.High {
			t.Fatalf("%s under a high group triages at %q", id, got)
		}
	}
	if err := st.MoveSession("b", "other"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if sess, _ := st.Get("b"); EffectiveTier(tiers, sess) != priority.Unset {
		t.Fatal("a session moved out of the group kept its tier")
	}
	if err := st.SetGroupPriority("proj", priority.Unset); err != nil {
		t.Fatalf("clear group priority: %v", err)
	}
	if sess, _ := st.Get("a"); EffectiveTier(groupTiers(t, st), sess) != priority.Unset {
		t.Fatal("clearing the group left its session tiered")
	}
}

// The higher of the two wins, whichever side states it: a group cannot
// demote a session that has declared itself urgent, and a session cannot
// escape an urgent group by stating something lower.
func TestEffectiveTierTakesTheHigherOfSessionAndGroup(t *testing.T) {
	st := newTestStore(t)
	st.CreateGroup("proj", "")
	st.CreateSession(sample("a", "proj"))

	if err := st.SetPriority("a", priority.Urgent); err != nil {
		t.Fatalf("set priority: %v", err)
	}
	if err := st.SetGroupPriority("proj", priority.Low); err != nil {
		t.Fatalf("set group priority: %v", err)
	}
	sess, _ := st.Get("a")
	if got := EffectiveTier(groupTiers(t, st), sess); got != priority.Urgent {
		t.Fatalf("low group demoted an urgent session to %q", got)
	}

	if err := st.SetPriority("a", priority.Low); err != nil {
		t.Fatalf("set priority: %v", err)
	}
	if err := st.SetGroupPriority("proj", priority.Urgent); err != nil {
		t.Fatalf("set group priority: %v", err)
	}
	sess, _ = st.Get("a")
	if got := EffectiveTier(groupTiers(t, st), sess); got != priority.Urgent {
		t.Fatalf("a low session escaped an urgent group at %q", got)
	}
}

// Clearing a tier sticks. The flag column the tiers replaced is still on
// the table and still reads 1 on a row marked before the upgrade, so a
// backfill that only looked at the tier would re-raise it on every start.
func TestClearedTierSurvivesTheLegacyBackfill(t *testing.T) {
	st := newTestStore(t)
	st.CreateSession(sample("a", ""))

	if _, err := st.db.Exec(`UPDATE sessions SET priority = 1, priority_tier = '' WHERE id = 'a'`); err != nil {
		t.Fatalf("stage a pre-tier mark: %v", err)
	}
	if err := st.init(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if sess, _ := st.Get("a"); sess.Priority != priority.High {
		t.Fatalf("a mark made before tiers came up as %q, want high", sess.Priority)
	}
	if err := st.SetPriority("a", priority.Unset); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := st.init(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if sess, _ := st.Get("a"); sess.Priority != priority.Unset {
		t.Fatalf("the backfill resurrected a cleared tier as %q", sess.Priority)
	}
}

func TestSetGroupPriorityRefusesRootAndUnknown(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetGroupPriority("", priority.High); err == nil {
		t.Fatal("root accepted a priority tier")
	}
	if err := st.SetGroupPriority("nope", priority.High); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("unknown group: err = %v, want ErrGroupNotFound", err)
	}
}
