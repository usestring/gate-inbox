package sysstat

import (
	"testing"
	"time"
)

// TestTheProcessTreeFixtureOutlivesNothing is what keeps the leak from coming
// back. The fixtures were leaking a busy-loop shell per run for long enough
// that a board accumulated 25 of them, and nothing failed while it happened:
// the tests they belong to passed either way.
//
// The tree is built inside a subtest so the cleanup under test has actually
// run by the time the counts are compared -- t.Run returns only after the
// subtest's t.Cleanup stack unwinds.
func TestTheProcessTreeFixtureOutlivesNothing(t *testing.T) {
	before := fixtureProcs()

	var peak int
	t.Run("a measured tree", func(t *testing.T) {
		startFixture(t, `while :; do :; done & sh -c 'sleep 60' "$0" & wait`)
		// The shell, its forked busy-loop subshell and the marked sleeper
		// all have to be up before the count means anything.
		peak = awaitAtLeast(before+3, 10*time.Second)
	})

	// Without this the test passes on a board where the fixture never
	// started at all, which is how a guard quietly stops guarding.
	if peak <= before {
		t.Fatalf("the fixture never started (%d -> %d marked processes): this guard would pass no matter what leaked", before, peak)
	}

	after := awaitFixtureProcs(before, 10*time.Second)
	if after > before {
		t.Fatalf("the fixture leaked %d process(es): %d marked before, %d at peak, %d after cleanup. "+
			"A backgrounded child outlives the shell that started it, so cleanup has to kill the process group",
			after-before, before, peak, after)
	}
}

// awaitAtLeast waits for the marked-process count to reach want, and returns
// whatever it reached. Used to give the fixture's children time to appear
// rather than to assert: the assertion is in the caller.
func awaitAtLeast(want int, within time.Duration) int {
	deadline := time.Now().Add(within)
	for {
		got := fixtureProcs()
		if got >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}
