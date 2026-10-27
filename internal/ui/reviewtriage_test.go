package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// reviewAsk is a pending ask somebody made age before smartNow, ingested
// at smartNow.
func reviewAsk(number int, age time.Duration) store.ReviewRequest {
	return store.ReviewRequest{Repo: "example/api", Number: number, Trigger: "requested",
		State: store.ReviewPending, TriggeredAt: smartNow.Add(-age), CreatedAt: smartNow}
}

// reviewDrain walks the queue the way successive handovers do, naming the
// Reviews tab "reviews".
func reviewDrain(m *Model) []string {
	var handed []string
	left := ""
	tried := map[string]bool{}
	for {
		i, ok := m.nextTriageInput(left, tried)
		if !ok {
			return handed
		}
		if i == reviewsTurn {
			tried[reviewsTriedKey] = true
			handed = append(handed, "reviews")
			continue
		}
		left = m.rows[i].sess.ID
		tried[left] = true
		handed = append(handed, m.rows[i].sess.Name)
	}
}

func TestReviewQueuesByItsNewestAskAmongSessions(t *testing.T) {
	m := smartFleet(t,
		smartSess("asking", status.Waiting, 30*time.Minute),
		smartSess("done", status.Finished, time.Minute),
	)
	m.reviewQueue = true
	m.reviews = []store.ReviewRequest{reviewAsk(2, 20*time.Minute), reviewAsk(1, 3*time.Hour)}
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"done", "reviews", "asking"}; !slices.Equal(got, want) {
		t.Fatalf("a twenty-minute-old ask beside a three-hour-old one: drain = %v, want %v", got, want)
	}

	m.reviews = []store.ReviewRequest{reviewAsk(1, 30*time.Second)}
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"reviews", "done", "asking"}; !slices.Equal(got, want) {
		t.Fatalf("a thirty-second-old ask: drain = %v, want %v", got, want)
	}
}

func TestReviewsTabLeavesTheDrainOnceSeenAndReturnsForANewAsk(t *testing.T) {
	m := smartFleet(t, smartSess("asking", status.Waiting, 30*time.Minute))
	m.reviewQueue = true
	m.reviews = []store.ReviewRequest{reviewAsk(1, 3*time.Hour)}
	m.rebuildRows()
	m.markReviewsSeen()
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"asking"}; !slices.Equal(got, want) {
		t.Fatalf("after the tab was seen: drain = %v, want %v", got, want)
	}

	later := reviewAsk(2, time.Minute)
	later.CreatedAt = smartNow.Add(time.Minute)
	m.reviews = append(m.reviews, later)
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"reviews", "asking"}; !slices.Equal(got, want) {
		t.Fatalf("after a new ask: drain = %v, want %v", got, want)
	}
}

func TestStartedAsksDoNotQueueTheReviewsTab(t *testing.T) {
	m := smartFleet(t, smartSess("asking", status.Waiting, 30*time.Minute))
	started := reviewAsk(1, 3*time.Hour)
	started.State = store.ReviewStarted
	m.reviewQueue = true
	m.reviews = []store.ReviewRequest{started}
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"asking"}; !slices.Equal(got, want) {
		t.Fatalf("drain = %v, want %v: a started ask queues through its session", got, want)
	}
}

func TestTriageHandsOverTheReviewsTabAndLeavingItMovesOn(t *testing.T) {
	m := smartFleet(t, smartSess("busy", status.Working, 30*time.Minute))
	m.reviewQueue = true
	m.reviews = []store.ReviewRequest{reviewAsk(1, 3*time.Hour), reviewAsk(2, time.Hour)}
	m.markReviewsSeen()
	fresh := reviewAsk(3, time.Minute)
	fresh.CreatedAt = smartNow.Add(time.Minute)
	m.reviews = append(m.reviews, fresh)
	m.rebuildRows()

	m.advanceTriage("")
	if !m.onReviewsTab() {
		t.Fatal("the drain did not hand over the Reviews tab")
	}
	if m.reviewsCursor != 2 {
		t.Fatalf("tab cursor = %d, want 2: the ask the drain had not handed over yet", m.reviewsCursor)
	}

	m.leaveReviewsTab(true)
	if m.onReviewsTab() {
		t.Fatal("leaving the tab with nothing else waiting stayed on it")
	}
	if m.reviewsWaiting() {
		t.Fatal("leaving the tab did not mark its asks seen")
	}
}

func TestTheReviewQueueStaysOffUntilItsExperimentIsOn(t *testing.T) {
	m := smartFleet(t, smartSess("asking", status.Waiting, 30*time.Minute))
	m.reviews = []store.ReviewRequest{reviewAsk(1, 3*time.Hour)}
	m.rebuildRows()
	m.toggleReviewsTab()
	if m.onReviewsTab() {
		t.Fatal("the Reviews tab opened with the experiment off")
	}
	if got := reviewDrain(m); slices.Contains(got, "reviews") {
		t.Fatalf("drain = %v: the Reviews tab queued with the experiment off", got)
	}
	if cmd := m.ingestReviews(); cmd != nil || m.reviewsFetching {
		t.Fatal("the ingest key fetched reviews with the experiment off")
	}
}
