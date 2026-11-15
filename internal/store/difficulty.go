package store

// A review's difficulty is how much of the operator one pull request will take,
// told apart from its priority, which is how much it matters. The queue still
// orders by priority; difficulty is there for the operator who has ten minutes
// and wants the reviews that fit in them.
//
// The tier is guessed from what an ask already stores -- the lines and files it
// changes and whether its checks fail -- and a tier set by hand outranks the
// guess, because the operator has read the diff and the counts have not.

// Difficulty tiers, easiest first. Empty is no tier: on a stored ask it means
// the guess stands, and as a filter it means every tier.
const (
	DifficultyTrivial = "trivial"
	DifficultyEasy    = "easy"
	DifficultyMedium  = "medium"
	DifficultyHard    = "hard"
)

// Difficulties is every tier, easiest first.
var Difficulties = []string{DifficultyTrivial, DifficultyEasy, DifficultyMedium, DifficultyHard}

// ReviewDifficultyFilterSetting persists the hardest tier the review queue
// hands over. Empty hands over every tier.
const ReviewDifficultyFilterSetting = "review_difficulty_filter"

// DifficultyRank is a tier's place on the scale, easiest 0. An unknown or
// empty tier ranks past the hardest, so it never passes a filter by accident.
func DifficultyRank(tier string) int {
	for i, d := range Difficulties {
		if d == tier {
			return i
		}
	}
	return len(Difficulties)
}

// difficultyBounds are the most lines and files each tier below hard may
// change; anything past medium's is hard.
var difficultyBounds = []struct{ lines, files int }{
	{20, 2},   // trivial
	{150, 6},  // easy
	{600, 20}, // medium
}

// GuessDifficulty is the tier an ask's size and checks suggest. Failing checks
// push it up one tier: the review has to work out whether the change or the
// test is wrong. An ask with no size known, such as one a session raised
// before the pull request was fetched, has no guess: it is empty, which no
// difficulty filter lets through.
func GuessDifficulty(r ReviewRequest) string {
	lines := r.Additions + r.Deletions
	if lines == 0 && r.ChangedFiles == 0 {
		return ""
	}
	rank := len(difficultyBounds)
	for i, b := range difficultyBounds {
		if lines <= b.lines && r.ChangedFiles <= b.files {
			rank = i
			break
		}
	}
	if r.Checks == "failing" {
		rank = min(rank+1, len(Difficulties)-1)
	}
	return Difficulties[rank]
}

// EffectiveDifficulty is the tier set by hand, or the guess when none is.
func (r ReviewRequest) EffectiveDifficulty() string {
	if DifficultyRank(r.Difficulty) < len(Difficulties) {
		return r.Difficulty
	}
	return GuessDifficulty(r)
}

// NextDifficulty is the tier after tier in the cycle a key walks: the guess,
// then each tier easiest first, then back to the guess.
func NextDifficulty(tier string) string {
	rank := DifficultyRank(tier)
	if rank >= len(Difficulties)-1 {
		if rank == len(Difficulties)-1 {
			return ""
		}
		return Difficulties[0]
	}
	return Difficulties[rank+1]
}

// NextDifficultyFilter is the ceiling after ceiling: every tier, then up to
// trivial, easy and medium, then every tier again. Up to hard is every tier,
// so the cycle skips it.
func NextDifficultyFilter(ceiling string) string {
	rank := DifficultyRank(ceiling)
	if rank >= len(Difficulties)-2 {
		if rank == len(Difficulties)-2 {
			return ""
		}
		return Difficulties[0]
	}
	return Difficulties[rank+1]
}

// SetPullRequestDifficulty sets the tier of every ask on one pull request.
// Empty goes back to the guess.
func (s *Store) SetPullRequestDifficulty(repo string, number int, tier string) error {
	_, err := s.db.Exec(`UPDATE review_requests SET difficulty = ?
		WHERE repo = ? AND number = ?`,
		tier, repo, number)
	return err
}
