package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// With no difficulty filter the drain is unchanged; with one, an ask past it
// neither queues the tab nor is the one handed over.
func TestReviewDifficultyFilterNarrowsTheDrain(t *testing.T) {
	m := smartFleet(t, smartSess("asking", status.Waiting, 30*time.Minute))
	m.reviewQueue = true
	hard := reviewAsk(1, 3*time.Hour)
	hard.Additions, hard.ChangedFiles = 3000, 50
	m.replaceReviews([]store.ReviewRequest{hard})
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"asking", "reviews"}; !slices.Equal(got, want) {
		t.Fatalf("no filter: drain = %v, want %v", got, want)
	}

	m.reviewCeiling = store.DifficultyEasy
	m.replaceReviews(m.reviews)
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"asking"}; !slices.Equal(got, want) {
		t.Fatalf("only a hard ask under up to easy: drain = %v, want %v", got, want)
	}

	easy := reviewAsk(2, time.Minute)
	easy.Additions, easy.ChangedFiles = 5, 1
	m.replaceReviews(append(m.reviews, easy))
	if m.reviews[0].Number != 2 {
		t.Fatalf("under a filter the ask it lets through leads the tab, got #%d first", m.reviews[0].Number)
	}
	m.handOverReviews()
	if r, _ := m.selectedReview(); r.Number != 2 {
		t.Fatalf("handover picked #%d, want the easy #2", r.Number)
	}
}

func TestDifficultyBadgeMarksAHandSetTier(t *testing.T) {
	r := store.ReviewRequest{Additions: 3, ChangedFiles: 1}
	if got := difficultySpan(r).Text; got != "trivial" {
		t.Fatalf("guessed badge = %q, want trivial", got)
	}
	r.Difficulty = store.DifficultyHard
	if got := difficultySpan(r).Text; got != "hard*" {
		t.Fatalf("hand-set badge = %q, want hard*", got)
	}
}

// A narrow rail drops the count before it cuts the filter's name.
func TestReviewTabBarKeepsTheFilterWhenNarrow(t *testing.T) {
	m := smartFleet(t)
	m.reviewQueue, m.reviewsTab = true, true
	m.replaceReviews([]store.ReviewRequest{reviewAsk(1, time.Minute)})
	m.reviewCeiling = store.DifficultyMedium
	if got := ansi.Strip(m.reviewsTabBar(36)); !strings.Contains(got, "Reviews · max medium") {
		t.Fatalf("narrow tab bar = %q, want the filter named in full", got)
	}
	if got := ansi.Strip(m.reviewsTabBar(80)); !strings.Contains(got, "Reviews (1) · max medium") {
		t.Fatalf("wide tab bar = %q, want the count and the filter", got)
	}
}
