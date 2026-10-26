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
// Reviews row "reviews".
func reviewDrain(m *Model) []string {
	var handed []string
	left := ""
	tried := map[string]bool{}
	for {
		i, ok := m.nextTriageInput(left, tried)
		if !ok {
			return handed
		}
		if m.rows[i].reviews {
			tried[reviewsTriedKey] = true
			handed = append(handed, "reviews")
			continue
		}
		left = m.rows[i].sess.ID
		tried[left] = true
		handed = append(handed, m.rows[i].sess.Name)
	}
}

func TestReviewQueuesByItsOldestAskAmongSessions(t *testing.T) {
	m := waitOnly(smartFleet(t,
		smartSess("asking", status.Waiting, 30*time.Minute),
		smartSess("done", status.Finished, time.Minute),
	))
	m.reviews = []store.ReviewRequest{reviewAsk(2, 20*time.Minute), reviewAsk(1, 3*time.Hour)}
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"reviews", "asking", "done"}; !slices.Equal(got, want) {
		t.Fatalf("a three-hour-old ask: drain = %v, want %v", got, want)
	}

	m.reviews = []store.ReviewRequest{reviewAsk(1, 2*time.Minute)}
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"asking", "reviews", "done"}; !slices.Equal(got, want) {
		t.Fatalf("a two-minute-old ask: drain = %v, want %v", got, want)
	}
}

func TestReviewRowLeavesTheDrainOnceSeenAndReturnsForANewAsk(t *testing.T) {
	m := smartFleet(t, smartSess("asking", status.Waiting, 30*time.Minute))
	m.reviews = []store.ReviewRequest{reviewAsk(1, 3*time.Hour)}
	m.rebuildRows()
	m.markReviewsSeen()
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"asking"}; !slices.Equal(got, want) {
		t.Fatalf("after the page was seen: drain = %v, want %v", got, want)
	}
	if !slices.ContainsFunc(m.rows, func(r treeRow) bool { return r.reviews }) {
		t.Fatal("a seen Reviews row left the rail; it should stay, out of the drain")
	}

	later := reviewAsk(2, time.Minute)
	later.CreatedAt = smartNow.Add(time.Minute)
	m.reviews = append(m.reviews, later)
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"reviews", "asking"}; !slices.Equal(got, want) {
		t.Fatalf("after a new ask: drain = %v, want %v", got, want)
	}
}

func TestStartedAsksDoNotQueueTheReviewsRow(t *testing.T) {
	m := smartFleet(t, smartSess("asking", status.Waiting, 30*time.Minute))
	started := reviewAsk(1, 3*time.Hour)
	started.State = store.ReviewStarted
	m.reviews = []store.ReviewRequest{started}
	m.rebuildRows()
	if got, want := reviewDrain(m), []string{"asking"}; !slices.Equal(got, want) {
		t.Fatalf("drain = %v, want %v: a started ask queues through its session", got, want)
	}
}

func TestTriageHandsOverTheReviewPageAndLeavingItMovesOn(t *testing.T) {
	m := smartFleet(t, smartSess("busy", status.Working, 30*time.Minute))
	m.reviews = []store.ReviewRequest{reviewAsk(1, 3*time.Hour), reviewAsk(2, time.Hour)}
	m.markReviewsSeen()
	fresh := reviewAsk(3, time.Minute)
	fresh.CreatedAt = smartNow.Add(time.Minute)
	m.reviews = append(m.reviews, fresh)
	m.rebuildRows()

	m.advanceTriage("")
	if m.mode != modeHeadPane || m.extView.id != reviewsPaneID {
		t.Fatalf("the drain did not hand over the review page: mode %v, pane %d", m.mode, m.extView.id)
	}
	if got := m.extView.view.(*reviewsView).cursor; got != 2 {
		t.Fatalf("page cursor = %d, want 2: the ask the drain had not handed over yet", got)
	}

	m.leaveHeadPane(true)
	if m.mode == modeHeadPane {
		t.Fatal("leaving the page with nothing else waiting stayed on it")
	}
	if m.reviewsWaiting() {
		t.Fatal("leaving the page did not mark its asks seen")
	}
}
