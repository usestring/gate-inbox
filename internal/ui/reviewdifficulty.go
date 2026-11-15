package ui

// Review difficulty on the Reviews tab. Priority still orders the queue, so an
// operator who never touches difficulty sees no change but a badge. The filter
// is for the other case: with a short gap, hand over only the reviews that fit
// in it. Reviews past the filter stay on the tab, below the rest, and the
// drain passes them by. See store/difficulty.go for the tiers and the guess.

import (
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/store"
)

func storedReviewCeiling(st *store.Store) string {
	ceiling, err := st.Setting(store.ReviewDifficultyFilterSetting)
	if err != nil || store.DifficultyRank(ceiling) >= len(store.Difficulties)-1 {
		return ""
	}
	return ceiling
}

// reviewInCeiling is whether the difficulty filter lets the ask through.
func (m *Model) reviewInCeiling(r store.ReviewRequest) bool {
	return m.reviewCeiling == "" ||
		store.DifficultyRank(r.EffectiveDifficulty()) <= store.DifficultyRank(m.reviewCeiling)
}

// reviewQueued is an ask the drain has yet to hand over: pending, come in
// since the tab was last handed over, and within the difficulty filter.
func (m *Model) reviewQueued(r store.ReviewRequest) bool {
	return r.State == store.ReviewPending && r.CreatedAt.After(m.reviewsSeen) && m.reviewInCeiling(r)
}

// reviewCeilingLabel names the filter for the status bar.
func reviewCeilingLabel(ceiling string) string {
	if ceiling == "" {
		return "any difficulty"
	}
	return "up to " + ceiling
}

// cycleReviewCeiling moves the filter one step: every tier, then up to
// trivial, easy and medium.
func (m *Model) cycleReviewCeiling() tea.Cmd {
	next := store.NextDifficultyFilter(m.reviewCeiling)
	if err := m.store.SetSetting(store.ReviewDifficultyFilterSetting, next); err != nil {
		m.errBar.text = "review queue: " + err.Error()
		return nil
	}
	m.reviewCeiling = next
	m.replaceReviews(m.reviews)
	m.reviewsCursor = 0
	m.reportDone("reviews: " + reviewCeilingLabel(next))
	return nil
}

// cycleReviewDifficulty moves the pull request's tier one step: the guess,
// then each tier easiest first. It is set on the pull request, not the ask,
// so every ask on it moves together.
func (m *Model) cycleReviewDifficulty(r store.ReviewRequest) tea.Cmd {
	next := store.NextDifficulty(r.Difficulty)
	if err := m.store.SetPullRequestDifficulty(r.Repo, r.Number, next); err != nil {
		m.errBar.text = "review queue: " + err.Error()
		return nil
	}
	if err := m.reloadReviewRequests(); err != nil {
		m.errBar.text = "review queue: " + err.Error()
		return nil
	}
	label := next
	if label == "" {
		label = "no difficulty guess"
		if guess := store.GuessDifficulty(r); guess != "" {
			label = "guessed " + guess
		}
	}
	m.reportDone(r.Repo + "#" + strconv.Itoa(r.Number) + ": " + label)
	return nil
}

// difficultySpan is the badge for an ask's tier. A tier set by hand carries a
// star so a guess is never mistaken for a judgement.
func difficultySpan(r store.ReviewRequest) Span {
	text := r.EffectiveDifficulty()
	if text == "" {
		return Span{}
	}
	if r.Difficulty != "" {
		text += "*"
	}
	tone := ToneMuted
	switch store.DifficultyRank(r.EffectiveDifficulty()) {
	case 0, 1:
		tone = ToneGood
	case 3:
		tone = ToneWarn
	}
	return Span{Text: text, Tone: tone}
}
