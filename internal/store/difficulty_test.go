package store

import (
	"slices"
	"testing"
	"time"
)

func TestGuessDifficultyFromSizeAndChecks(t *testing.T) {
	cases := []struct {
		name string
		r    ReviewRequest
		want string
	}{
		{"one-line fix", ReviewRequest{Additions: 3, Deletions: 1, ChangedFiles: 1}, DifficultyTrivial},
		{"small change across many files", ReviewRequest{Additions: 10, ChangedFiles: 5}, DifficultyEasy},
		{"a feature", ReviewRequest{Additions: 400, Deletions: 100, ChangedFiles: 12}, DifficultyMedium},
		{"a rewrite", ReviewRequest{Additions: 2000, ChangedFiles: 40}, DifficultyHard},
		{"few lines over too many files", ReviewRequest{Additions: 40, ChangedFiles: 30}, DifficultyHard},
		{"failing checks push a tier up", ReviewRequest{Additions: 3, ChangedFiles: 1, Checks: "failing"}, DifficultyEasy},
		{"no size known", ReviewRequest{Trigger: "session"}, ""},
		{"failing checks stop at hard", ReviewRequest{Additions: 2000, ChangedFiles: 40, Checks: "failing"}, DifficultyHard},
	}
	for _, c := range cases {
		if got := GuessDifficulty(c.r); got != c.want {
			t.Errorf("%s: GuessDifficulty = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDifficultyCycles(t *testing.T) {
	walk := func(next func(string) string, steps int) []string {
		var got []string
		tier := ""
		for range steps {
			tier = next(tier)
			got = append(got, tier)
		}
		return got
	}
	if got, want := walk(NextDifficulty, 6), []string{"trivial", "easy", "medium", "hard", "", "trivial"}; !slices.Equal(got, want) {
		t.Fatalf("NextDifficulty walk = %q, want %q", got, want)
	}
	if got, want := walk(NextDifficultyFilter, 5), []string{"trivial", "easy", "medium", "", "trivial"}; !slices.Equal(got, want) {
		t.Fatalf("NextDifficultyFilter walk = %q, want %q", got, want)
	}
}

// A tier set by hand outranks the guess, survives a refetch, and carries over
// to a new ask on the same pull request.
func TestHandSetDifficultySurvivesIngest(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1700000000, 0)
	ask := ReviewRequest{Repo: "o/r", Number: 7, Trigger: "comment", TriggerID: "C_1", Additions: 2, ChangedFiles: 1}
	if _, err := s.UpsertReviewRequests([]ReviewRequest{ask}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPullRequestDifficulty("o/r", 7, DifficultyHard); err != nil {
		t.Fatal(err)
	}
	again := ask
	again.TriggerID = "C_2"
	added, err := s.UpsertReviewRequests([]ReviewRequest{ask, again}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Difficulty != DifficultyHard {
		t.Fatalf("new ask on a tiered pull request = %+v, want difficulty hard", added)
	}
	open, err := s.OpenReviewRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Fatalf("open = %d asks, want 2", len(open))
	}
	for _, r := range open {
		if r.Difficulty != DifficultyHard || r.EffectiveDifficulty() != DifficultyHard {
			t.Fatalf("%s: difficulty %q effective %q, want hard", r.TriggerID, r.Difficulty, r.EffectiveDifficulty())
		}
	}

	if err := s.SetPullRequestDifficulty("o/r", 7, ""); err != nil {
		t.Fatal(err)
	}
	open, err = s.OpenReviewRequests()
	if err != nil {
		t.Fatal(err)
	}
	if open[0].EffectiveDifficulty() != DifficultyTrivial {
		t.Fatalf("cleared tier: effective %q, want the guess trivial", open[0].EffectiveDifficulty())
	}
}

func TestHandSetDifficultySurvivesClosedAsks(t *testing.T) {
	for _, state := range []string{ReviewDismissed, ReviewSubmitted} {
		t.Run(state, func(t *testing.T) {
			s := newTestStore(t)
			now := time.Unix(1700000000, 0)
			ask := ReviewRequest{Repo: "o/r", Number: 7, Trigger: "comment", TriggerID: "initial", Additions: 2, ChangedFiles: 1}
			if _, err := s.UpsertReviewRequests([]ReviewRequest{ask}, now); err != nil {
				t.Fatal(err)
			}
			for _, tier := range []string{DifficultyHard, DifficultyEasy, ""} {
				if err := s.SetPullRequestDifficulty(ask.Repo, ask.Number, tier); err != nil {
					t.Fatal(err)
				}
				if err := s.SetPullRequestReviewState(ask.Repo, ask.Number, state, "", "", now); err != nil {
					t.Fatal(err)
				}
				ask.TriggerID += "-next"
				added, err := s.UpsertReviewRequests([]ReviewRequest{ask}, now)
				if err != nil {
					t.Fatal(err)
				}
				if len(added) != 1 || added[0].Difficulty != tier {
					t.Fatalf("new ask after %s: %+v, want difficulty %q", state, added, tier)
				}
				open, err := s.OpenReviewRequests()
				if err != nil {
					t.Fatal(err)
				}
				if len(open) != 1 || open[0].TriggerID != ask.TriggerID {
					t.Fatalf("open asks = %+v, want only %s", open, ask.TriggerID)
				}
				want := tier
				if want == "" {
					want = DifficultyTrivial
				}
				if got := open[0].EffectiveDifficulty(); got != want {
					t.Fatalf("effective difficulty = %q, want %q", got, want)
				}
			}
		})
	}
}
