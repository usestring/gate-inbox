package scoring

import (
	"math"
	"strings"
	"testing"
	"time"
)

var epoch = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func used(v float64) *float64 { return &v }

// week is a reading with the week used and resetting weekLeft from epoch,
// under a five-hour window used fiveHour.
func week(fiveHour, week float64, weekLeft time.Duration) Usage {
	return Usage{Windows: map[string]Window{
		FiveHour: {Utilization: used(fiveHour), ResetsAt: epoch.Add(5 * time.Hour)},
		SevenDay: {Utilization: used(week), ResetsAt: epoch.Add(weekLeft)},
	}}
}

func TestHeadroomRefusesReadingsThatCannotAuthorize(t *testing.T) {
	now := epoch
	for _, tc := range []struct {
		name   string
		change func(*Usage)
		reason string
	}{
		{"fresh", func(*Usage) {}, ""},
		{"stale", func(u *Usage) { u.ObservedAt = now.Add(-3 * time.Minute) }, "stale"},
		{"missing week", func(u *Usage) { delete(u.Windows, SevenDay) }, "unread"},
		{"unknown", func(u *Usage) {
			u.Windows[FiveHour] = Window{ResetsAt: now.Add(time.Hour)}
		}, "unread"},
		{"exhausted", func(u *Usage) {
			u.Windows[FiveHour] = Window{Utilization: used(100), ResetsAt: now.Add(time.Hour)}
		}, "limit"},
		{"week reserve", func(u *Usage) {
			u.Windows[SevenDay] = Window{Utilization: used(86), ResetsAt: now.Add(7 * 24 * time.Hour)}
		}, "reserve"},
		{"unused", func(u *Usage) { u.Windows[FiveHour] = Window{Utilization: used(0)} }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := week(20, 20, 7*24*time.Hour)
			tc.change(&u)
			st := Headroom(u, now)
			if st.Usable != (tc.reason == "") || st.Reason != tc.reason {
				t.Fatalf("Headroom = %+v, want reason %q", st, tc.reason)
			}
		})
	}
}

// Only the weekly window scores, however tight the five-hour one is: its
// score is the headroom left over the square of the hours until it resets.
func TestHeadroomScoresTheWeekAndClimbsAsItsResetNears(t *testing.T) {
	st := Headroom(week(80, 70, 42*time.Hour), epoch)
	if !st.Usable || st.Window != SevenDay || !st.ResetsAt.Equal(epoch.Add(42*time.Hour)) {
		t.Fatalf("Headroom = %+v", st)
	}
	// 95 less a reserve of 10 * 42/168 of the week, less the 70 used, over
	// 42 hours squared.
	if st.Headroom < 22.4 || st.Headroom > 22.6 || st.Score < 0.0127 || st.Score > 0.0128 {
		t.Fatalf("headroom %v score %v, want 22.5 and 0.01276", st.Headroom, st.Score)
	}
	if got := HeadroomScore(st.Headroom, 42*time.Hour).String(); !strings.HasPrefix(got, "headroom 0.0128") {
		t.Fatalf("breakdown %q", got)
	}
	// The same headroom ten times nearer its reset scores a hundred times
	// higher; minutes from it the score is floored, not infinite.
	far, near := Headroom(week(5, 50, 40*time.Hour), epoch), Headroom(week(5, 50, 4*time.Hour), epoch)
	if ratio := near.Score / far.Score; ratio < 100 || ratio > 110 {
		t.Fatalf("score ratio %v for a reset ten times nearer", ratio)
	}
	if last := Headroom(week(5, 50, time.Minute), epoch); math.IsInf(last.Score, 0) || last.Score > 16*45 {
		t.Fatalf("score a minute from reset = %v", last.Score)
	}
}

func TestHeadroomRanksTheQuotaClosestToBeingLost(t *testing.T) {
	fresh := 7 * 24 * time.Hour
	for _, tc := range []struct {
		name      string
		win, lose Usage
	}{
		{"same reset: the more headroom", week(5, 10, fresh), week(5, 60, fresh)},
		{"the sooner reset, with headroom left", week(5, 40, 30*time.Hour), week(5, 10, fresh)},
		{"a sooner reset with little left loses", week(5, 10, 48*time.Hour), week(5, 85, 24*time.Hour)},
		{"a nearer reset with less left beats a later one with more", week(5, 80, 12*time.Hour), week(5, 10, 48*time.Hour)},
		{"an hour from reset outscores two days", week(5, 90, time.Hour), week(5, 10, 48*time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			win, lose := Headroom(tc.win, epoch), Headroom(tc.lose, epoch)
			if !win.Usable || !lose.Usable || win.Score <= lose.Score {
				t.Fatalf("win %+v, lose %+v", win, lose)
			}
		})
	}
}

func TestWorthSwitchingOnlyForAMuchHigherScore(t *testing.T) {
	score := func(u Usage) float64 { return Headroom(u, epoch).Score }
	if from, to := score(week(5, 10, 7*24*time.Hour)), score(week(5, 40, 30*time.Hour)); !WorthSwitching(from, to) {
		t.Fatalf("%v to %v: a much higher score did not switch", from, to)
	}
	if from, to := score(week(5, 10, 48*time.Hour)), score(week(5, 12, 40*time.Hour)); to <= from || WorthSwitching(from, to) {
		t.Fatalf("%v to %v: want a higher score inside the margin, not switched", from, to)
	}
}
