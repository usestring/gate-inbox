package autoroute

import (
	"context"
	"errors"
	"testing"
	"time"
)

func reading(now time.Time, five, week, month float64) Reading {
	return Reading{ObservedAt: now, Windows: []Window{
		{Used: five, ResetsAt: now.Add(4 * time.Hour), Duration: 5 * time.Hour},
		{Used: week, ResetsAt: now.Add(6 * 24 * time.Hour), Duration: 7 * 24 * time.Hour},
		{Used: month, ResetsAt: now.Add(25 * 24 * time.Hour), Duration: 30 * 24 * time.Hour},
	}}
}

func TestChoosePacesEveryWindowAndReservesActiveWork(t *testing.T) {
	now := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)
	quota := map[string]Reading{
		"claude": reading(now, 5, 60, 10),
		"codex":  reading(now, 55, 15, 10),
	}
	router := New(func(_ context.Context, name string) (Reading, error) { return quota[name], nil })
	chosen, err := router.Choose(context.Background(), []string{"claude", "codex"}, nil, now)
	if err != nil || chosen != "codex" {
		t.Fatalf("choose = %q, %v; weekly pressure should outweigh Claude's low five-hour use", chosen, err)
	}
	router = New(func(_ context.Context, name string) (Reading, error) { return quota[name], nil })
	chosen, err = router.Choose(context.Background(), []string{"claude", "codex"}, map[string]int{"codex": 7}, now)
	if err != nil || chosen != "claude" {
		t.Fatalf("choose with busy Codex = %q, %v; reserved headroom should reject it", chosen, err)
	}
}

func TestChooseCachesOnlyFreshSuccessfulReads(t *testing.T) {
	now := time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC)
	reads := 0
	router := New(func(_ context.Context, _ string) (Reading, error) {
		reads++
		if reads == 1 {
			return Reading{}, errors.New("offline")
		}
		return reading(now, 10, 10, 10), nil
	})
	if _, err := router.Choose(context.Background(), []string{"claude"}, nil, now); !errors.Is(err, ErrNoQuota) {
		t.Fatalf("offline read = %v, want no quota", err)
	}
	for range 2 {
		if _, err := router.Choose(context.Background(), []string{"claude"}, nil, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 2 {
		t.Fatalf("read count = %d, want failure retried then success cached", reads)
	}
	if _, err := router.Choose(context.Background(), []string{"claude"}, nil, now.Add(3*time.Minute)); !errors.Is(err, ErrNoQuota) {
		t.Fatalf("stale reread = %v, want no quota", err)
	}
}

func TestScoreRejectsUnknownOrExhaustedQuota(t *testing.T) {
	now := time.Now()
	for _, sample := range []Reading{
		{ObservedAt: now.Add(-3 * time.Minute), Windows: reading(now, 10, 10, 10).Windows},
		{ObservedAt: now, Windows: []Window{{Used: 95, ResetsAt: now.Add(time.Hour), Duration: 5 * time.Hour}}},
		{ObservedAt: now, Windows: []Window{{Used: 20}}},
	} {
		if _, ok := Score(sample, 0, now); ok {
			t.Fatalf("accepted unusable quota: %+v", sample)
		}
	}
}
