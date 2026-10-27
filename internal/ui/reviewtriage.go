package ui

// The review queue in triage: the Reviews tab queues beside the sessions as
// one slot of its own, and a drain that reaches it switches the rail to the
// tab rather than focusing a pane.
//
// The slot stands for every pending ask at once, not one per pull request:
// the tab is where the asks are worked, and a drain that handed the same tab
// over once per ask would walk the operator back into it after each one. An
// ask that has a review session is off the count, because that session queues
// on its own.

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/store"
)

const triageReview = "review"

// reviewsTriedKey is the Reviews tab's place in a walk's tried set, which is
// keyed by session id.
const reviewsTriedKey = "\x00reviews"

// reviewsWaiting is whether a pending ask came in after the operator last
// left the tab a drain handed them. Leaving it is how a drain moves past it,
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

// markReviewsSeen is the operator leaving the tab a drain handed over.
func (m *Model) markReviewsSeen() {
	for _, r := range m.reviews {
		if r.State == store.ReviewPending && r.CreatedAt.After(m.reviewsSeen) {
			m.reviewsSeen = r.CreatedAt
		}
	}
}

// reviewsTriageKey is the Reviews tab's key in the triage order. In smart
// triage it queues as one slot of its own on the newest pending ask's recency,
// at the review rank and with no priority tier, since an ask carries none.
func (m *Model) reviewsTriageKey() triageKey {
	key := triageKey{band: triageBandRest, priority: priority.Unset.Rank(), tier: triageRank(triageReview)}
	if !m.reviewsWaiting() {
		return key
	}
	key.band = triageBandWalkable
	if !m.smartTriage {
		return key
	}
	var newest time.Time
	for _, r := range m.reviews {
		at := r.TriggeredAt
		if at.IsZero() {
			at = r.CreatedAt
		}
		if r.State == store.ReviewPending && at.After(newest) {
			newest = at
		}
	}
	key.band, key.score = triageBandScored, triageRecency(newest)
	key.tier = slices.Index(triageScoredTiers, triageReview)
	return key
}

// reviewsTriageAt is where the Reviews tab's slot falls in a triage queue:
// before the first session whose key it sorts ahead of. It is only ever placed
// at the head of a session's lineage -- above the header rows drawn over it --
// so it never splits a session from its children or its work.
func (m *Model) reviewsTriageAt(rows []treeRow, from int) int {
	now := m.now()
	key := m.reviewsTriageKey()
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

// handOverReviews switches the rail to the Reviews tab with its cursor on the
// oldest ask the drain has not handed over yet, and opens that ask's pull
// request. When every ask has been handed over before, the tab is left as it
// was.
func (m *Model) handOverReviews() (bool, tea.Cmd) {
	next := -1
	for i, r := range m.reviews {
		if r.State == store.ReviewPending && r.CreatedAt.After(m.reviewsSeen) {
			next = i
			break
		}
	}
	m.reviewsTab = true
	m.triageResume = false
	if next < 0 {
		return true, nil
	}
	m.reviewsCursor = next
	return true, m.openPullRequest(m.reviews[next])
}

// reviewsSlot is where a drain reaches the Reviews tab among m.rows, and
// whether it can at all: the experiment on, an ask come in since the tab was
// last handed over, no search narrowing the list, and not already tried in
// this advance.
func (m *Model) reviewsSlot(tried map[string]bool) (int, bool) {
	if !m.reviewQueue || m.search != "" || tried[reviewsTriedKey] || !m.reviewsWaiting() {
		return 0, false
	}
	from := 0
	if len(m.rows) > 0 && m.rows[0].isRoot() {
		from = 1
	}
	return m.reviewsTriageAt(m.rows, from), true
}
