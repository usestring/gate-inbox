package extensionhost

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Each extension's deadline is its own: the earliest across extensions is
// what triage sees, zero clears only the setter's, and a stopped extension
// takes its deadlines with it.
func TestQueueDeadlinesMergeAndLapse(t *testing.T) {
	events := NewEvents(NewBoard("", nil), nil)
	a, releaseA := events.For("ext-a")
	b, releaseB := events.For("ext-b")
	defer releaseB()
	now := time.Now()
	a.SetQueueDeadline("s1", now.Add(5*time.Minute))
	b.SetQueueDeadline("s1", now.Add(2*time.Minute))
	a.SetQueueDeadline("s2", now.Add(time.Minute))
	got := events.QueueDeadlines()
	if !got["s1"].Equal(now.Add(2*time.Minute)) || !got["s2"].Equal(now.Add(time.Minute)) {
		t.Fatalf("deadlines = %v", got)
	}
	b.SetQueueDeadline("s1", time.Time{})
	if got := events.QueueDeadlines(); !got["s1"].Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("clearing b's left %v, want a's", got["s1"])
	}
	releaseA()
	if got := events.QueueDeadlines(); len(got) != 0 {
		t.Fatalf("a stopped extension's deadlines stayed: %v", got)
	}
}

// An extension's MigrateAccount reaches the board through its view, and a
// context already done is refused before any session is touched; the move
// itself is sessioncmd's BoardMigrateAccount, tested there.
func TestMigrateAccountHonoursACancelledContext(t *testing.T) {
	events := NewEvents(NewBoard("", nil), nil)
	view, release := events.For("ext-a")
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := view.MigrateAccount(ctx, "s1", "ALICE1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("MigrateAccount on a done context = %v, want context.Canceled", err)
	}
}
