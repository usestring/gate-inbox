package store

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimitRecoveryClaimsSurviveCompetingPollers(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("limited", "g")); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("limited")
	if err != nil {
		t.Fatal(err)
	}
	state := LimitRecovery{Banner: "limit", ResetAt: time.Now(), LaunchAt: sess.LaunchTime()}
	if ok, err := st.CompareLimitRecovery(sess, nil, &state); err != nil || !ok {
		t.Fatalf("schedule: %v %v", ok, err)
	}
	states, err := st.LimitRecoveries()
	if err != nil || !states[sess.ID].ResetAt.Equal(state.ResetAt) {
		t.Fatalf("read: %v %v", states, err)
	}
	claimed := state
	claimed.AttemptedAt = time.Now()
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			ok, err := st.CompareLimitRecovery(sess, &state, &claimed)
			if err != nil {
				t.Error(err)
			}
			if ok {
				winners.Add(1)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d claim winners", winners.Load())
	}
	if ok, err := st.CompareLimitRecovery(sess, &claimed, nil); err != nil || !ok {
		t.Fatalf("clear: %v %v", ok, err)
	}
	if ok, err := st.CompareLimitRecovery(sess, &state, &claimed); err != nil || ok {
		t.Fatalf("stale claim resurrected cleared state: %v %v", ok, err)
	}
}

func TestLimitRecoveryRejectsChangedSession(t *testing.T) {
	for _, action := range []string{"archive", "restart", "delete"} {
		t.Run(action, func(t *testing.T) {
			st := newTestStore(t)
			if err := st.CreateSession(sample("limited", "g")); err != nil {
				t.Fatal(err)
			}
			sess, err := st.Get("limited")
			if err != nil {
				t.Fatal(err)
			}
			state := LimitRecovery{Banner: "limit", ResetAt: time.Now(), LaunchAt: sess.LaunchTime()}
			switch action {
			case "archive":
				err = st.SetArchived(sess.ID, true)
			case "restart":
				err = st.RestartAgent(sess.ID, "", time.Now().Add(time.Second))
			case "delete":
				err = st.Delete(sess.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := st.CompareLimitRecovery(sess, nil, &state); err != nil || ok {
				t.Fatalf("stale schedule: %v %v", ok, err)
			}
		})
	}
}

func TestLimitRecoveryAcceptsLegacySecondPrecisionSessions(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("legacy", "g")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE sessions SET created_at = ? WHERE id = 'legacy'`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("legacy")
	if err != nil {
		t.Fatal(err)
	}
	state := LimitRecovery{Banner: "limit", ResetAt: time.Now(), LaunchAt: sess.LaunchTime()}
	if ok, err := st.CompareLimitRecovery(sess, nil, &state); err != nil || !ok {
		t.Fatalf("legacy schedule: %v %v", ok, err)
	}
}
