package ui

import (
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestTriageContentKeepsTheDrainInTheChosenQueue(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    []string
	}{
		{triageContentBoth, []string{"asking", "reviews", "done", "briefing"}},
		{triageContentSessions, []string{"asking", "done"}},
		{triageContentReviews, []string{"reviews", "briefing"}},
	} {
		t.Run(tc.content, func(t *testing.T) {
			m := smartFleet(t,
				smartSess("asking", status.Waiting, time.Hour),
				smartSess("briefing", status.Finished, 30*time.Minute),
				smartSess("done", status.Finished, time.Minute),
			)
			m.reviewQueue = true
			started := reviewAsk(2, time.Hour)
			started.State, started.SessionID = store.ReviewStarted, "briefing"
			m.reviews = []store.ReviewRequest{reviewAsk(1, time.Hour), started}
			m.triageContent = tc.content
			m.rebuildRows()
			if got := reviewDrain(m); !slices.Equal(got, tc.want) {
				t.Fatalf("drain = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTriageContentShortcutPersistsAndSwitchesTabs(t *testing.T) {
	m := buildModel(t)
	m.reviewQueue = true
	if err := m.store.SetSetting(reviewQueueSetting, "on"); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{triageContentSessions, triageContentReviews, triageContentBoth} {
		m.handleKey(tea.KeyPressMsg{Code: 'I', Text: "I"})
		restored := New(m.cfg, m.store, m.tmux, m.engine, m.hooks, "test")
		if restored.triageContent != content || restored.onReviewsTab() != (content == triageContentReviews) {
			t.Fatalf("restored content = %q, reviews tab = %v for %s", restored.triageContent, restored.onReviewsTab(), content)
		}
		if content != triageContentBoth && m.onReviewsTab() != (content == triageContentReviews) {
			t.Fatalf("reviews tab = %v for %s", m.onReviewsTab(), content)
		}
	}
}

func TestTriageContentDoesNotPickUpExcludedArrivals(t *testing.T) {
	for _, content := range []string{triageContentSessions, triageContentReviews} {
		t.Run(content, func(t *testing.T) {
			m := smartFleet(t, smartSess("asking", status.Waiting, time.Hour))
			m.reviewQueue = true
			m.triageContent = content
			m.triageResume = true
			if content == triageContentSessions {
				m.sessions = nil
				m.reviews = []store.ReviewRequest{reviewAsk(1, time.Minute)}
				m.rebuildRows()
			}
			if cmd := m.triagePickupCmd(); cmd != nil || m.onReviewsTab() || !m.triageResume {
				t.Fatal("an excluded arrival took over the exhausted drain")
			}
		})
	}
}

func TestTriageContentPicksUpNewReviewAfterTheQueueEmpties(t *testing.T) {
	m := smartFleet(t)
	m.reviewQueue = true
	m.triageContent = triageContentReviews
	m.triageResume = true
	m.reviews = []store.ReviewRequest{reviewAsk(1, time.Minute)}
	if cmd := m.triagePickupCmd(); cmd == nil || !m.onReviewsTab() || m.triageResume {
		t.Fatal("a new review did not take over the waiting reviews-only drain")
	}
}

func TestTriageContentShortcutLeavesExcludedReviewFocus(t *testing.T) {
	m := smartFleet(t, smartSess("briefing", status.Finished, time.Minute))
	m.reviewQueue = true
	ask := reviewAsk(1, time.Minute)
	ask.State, ask.SessionID = store.ReviewStarted, "briefing"
	m.reviews = []store.ReviewRequest{ask}
	m.selectSessionRow(t, "briefing")
	m.mode = modeFocus
	m.handleKey(tea.KeyPressMsg{Code: ']', Mod: tea.ModAlt})
	if m.triageContent != triageContentSessions || m.mode != modeList || m.onReviewsTab() {
		t.Fatalf("mode switch left content=%s, mode=%v, reviews=%v", m.triageContent, m.mode, m.onReviewsTab())
	}
}
