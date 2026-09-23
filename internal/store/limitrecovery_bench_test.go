package store

import (
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkLimitRecoveryClaim(b *testing.B) {
	st, err := Open(filepath.Join(b.TempDir(), "claims.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { st.Close() })
	if err := st.CreateSession(Session{ID: "limited", Tool: "claude"}); err != nil {
		b.Fatal(err)
	}
	sess, err := st.Get("limited")
	if err != nil {
		b.Fatal(err)
	}
	state := LimitRecovery{Banner: "You've hit your limit", ResetAt: time.Now(), LaunchAt: sess.LaunchTime()}
	if ok, err := st.CompareLimitRecovery(sess, nil, &state); err != nil || !ok {
		b.Fatalf("seed: %v %v", ok, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		next := state
		next.AttemptedAt = time.Now()
		if ok, err := st.CompareLimitRecovery(sess, &state, &next); err != nil || !ok {
			b.Fatalf("claim: %v %v", ok, err)
		}
		state = next
	}
}
