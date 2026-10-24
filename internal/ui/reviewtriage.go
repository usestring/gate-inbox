package ui

// The review queue in triage: the Reviews row queues beside the sessions, and
// a drain that reaches it hands over the review page rather than a pane.
//
// The row stands for every pending ask at once, not one per pull request: the
// page is where the asks are worked, and a drain that handed the same page
// over once per ask would walk the operator back into it after each one. An
// ask that has a review session is off the count, because that session queues
// on its own.

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/store"
)

const triageReview = "review"

// reviewsTriedKey is the Reviews row's place in a walk's tried set, which is
// keyed by session id.
const reviewsTriedKey = "\x00reviews"

// reviewsWaiting is whether a pending ask came in after the operator last
// left the page a drain handed them. Leaving it is how a drain moves past it,
// the way leaving a session mutes that session, and a new ask is what brings
// it back.
func (m *Model) reviewsWaiting() bool {
	for _, r := range m.reviews {
		if r.State == store.ReviewPending && r.CreatedAt.After(m.reviewsSeen) {
			return true
		}
	}
	return false
}

// markReviewsSeen is the operator leaving the page a drain handed over.
func (m *Model) markReviewsSeen() {
	for _, r := range m.reviews {
		if r.State == store.ReviewPending && r.CreatedAt.After(m.reviewsSeen) {
			m.reviewsSeen = r.CreatedAt
		}
	}
}

// reviewsTriageKey is the Reviews row's key in the triage order. In smart
// triage it scores as one waiting row of its own: the oldest pending ask's
// wait at the review weight, with no priority tier, since an ask carries none.
func (m *Model) reviewsTriageKey(now time.Time) triageKey {
	key := triageKey{band: triageBandRest, priority: priority.Unset.Rank(), tier: triageRank(triageReview)}
	if !m.reviewsWaiting() {
		return key
	}
	key.band = triageBandWalkable
	if !m.smartTriage {
		return key
	}
	var oldest time.Time
	for _, r := range m.reviews {
		at := r.TriggeredAt
		if at.IsZero() {
			at = r.CreatedAt
		}
		if r.State == store.ReviewPending && (oldest.IsZero() || at.Before(oldest)) {
			oldest = at
		}
	}
	wait := max(now.Sub(oldest), 0) + triageScoreFloor
	key.band, key.score = triageBandScored, wait.Seconds()*triageStateWeight[triageReview]
	return key
}

// reviewsTriageAt is where the Reviews row goes in a triage queue: before the
// first session whose key it sorts ahead of. It is only ever placed at the head
// of a session's lineage -- above the header rows drawn over it -- so it never
// splits a session from its children or its work.
func (m *Model) reviewsTriageAt(rows []treeRow, from int) int {
	now := m.now()
	key := m.reviewsTriageKey(now)
	for i := from; i < len(rows); i++ {
		row := rows[i]
		if row.depth != 0 || !(row.isHead() || row.isSession()) {
			continue
		}
		if i > 0 && rows[i-1].isHead() && rows[i-1].sess.ID == row.sess.ID {
			continue
		}
		k, ok := m.triageLineage[row.sess.ID]
		if !ok {
			k = m.triageKeyOf(row.sess, now)
		}
		if key.before(k) {
			return i
		}
	}
	return len(rows)
}

// handOverReviews focuses the review page on the pull request of the oldest
// ask the drain has not handed over yet, or on the queue when every ask has
// been handed over before.
func (m *Model) handOverReviews() (bool, tea.Cmd) {
	view := m.reviewsPane().view.(*reviewsView)
	next := -1
	for i, r := range m.reviews {
		if r.State == store.ReviewPending && r.CreatedAt.After(m.reviewsSeen) {
			next = i
			break
		}
	}
	if !m.focusHeadPane() {
		return false, nil
	}
	if next < 0 {
		return true, nil
	}
	view.cursor = next
	return true, m.openPullRequest(m.reviews[next])
}

// withReviewsTriage puts the Reviews row into a triage queue where its key
// sorts it, rather than at the top where the tree keeps it.
func (m *Model) withReviewsTriage(rows []treeRow, query string) []treeRow {
	if len(m.reviews) == 0 || query != "" {
		return rows
	}
	from := 0
	if len(rows) > 0 && rows[0].isRoot() {
		from = 1
	}
	at := m.reviewsTriageAt(rows, from)
	return append(rows[:at:at], append([]treeRow{{reviews: true}}, rows[at:]...)...)
}

// reviewsHandable is whether a drain can hand the Reviews row over.
func (m *Model) reviewsHandable(row treeRow, tried map[string]bool) bool {
	return row.reviews && !tried[reviewsTriedKey] && m.reviewsWaiting()
}
