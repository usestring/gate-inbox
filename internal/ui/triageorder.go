package ui

import (
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Triage orders are the operator's answer to "what should the drain hand me
// next". Boards differ: one with a dozen agents blocked on permission
// dialogs wants every question first, one where a few urgent workstreams run
// at once wants priority to win, and one the operator walks every session of
// in turn wants nothing to jump the line at all. One fixed order served the
// first and failed the others, so the order is a setting.
const (
	// triageOrderQuestions puts a session stopped on a question ahead of
	// every other unmuted row, whatever its priority bar low. The default.
	triageOrderQuestions = "questions-first"
	// triageOrderPriority ranks priority above state: an urgent finish beats
	// an untiered question. Deadlines and keep-alive rests still order only
	// within one state, so this is the order before questions led, without
	// the warm sessions that crowded every question out of it.
	triageOrderPriority = "priority-first"
	// triageOrderOldest hands over whoever has waited longest since the
	// operator's last turn on it, ignoring state, priority and deadlines, so
	// every session comes round in turn.
	triageOrderOldest = "oldest-first"
	// triageOrderQuestionsOnly sorts like questions-first but the drain
	// hands over only sessions stopped on a question. Everything else stays
	// on the rail to open by hand.
	triageOrderQuestionsOnly = "questions-only"
)

var triageOrders = []string{triageOrderQuestions, triageOrderPriority, triageOrderOldest, triageOrderQuestionsOnly}

const (
	triageOrderSetting = "triage_order"
	// triageKeepAlivesSetting puts keep-alive rests back in the drain. Off
	// by default: a session whose only news is the cache warden's "ok" has
	// nothing for the operator, and handing it over is a hop spent on no
	// work. See chorerest.go.
	triageKeepAlivesSetting = "triage_keepalives"
)

func normalizeTriageOrder(order string) string {
	for _, known := range triageOrders {
		if known == order {
			return order
		}
	}
	return triageOrderQuestions
}

func storedTriageOrder(st *store.Store) string {
	chosen, err := st.Setting(triageOrderSetting)
	if err != nil {
		return triageOrderQuestions
	}
	return normalizeTriageOrder(chosen)
}

func storedTriageKeepAlives(st *store.Store) bool {
	chosen, err := st.Setting(triageKeepAlivesSetting)
	return err == nil && chosen == "on"
}

// triageOrderLabel is the order as the rail's footer and Settings show it.
func triageOrderLabel(order string) string {
	switch normalizeTriageOrder(order) {
	case triageOrderPriority:
		return "priority first"
	case triageOrderOldest:
		return "oldest first"
	case triageOrderQuestionsOnly:
		return "questions only"
	}
	return "questions first"
}

// inTriageRotation is whether the drain may hand sess over under the chosen
// order. It narrows the walk, never the rail: a session out of rotation
// keeps its row and its place, and opening it by hand works as before.
func (m *Model) inTriageRotation(sess store.Session) bool {
	if m.isChoreRest(sess) && !m.triageKeepAlives {
		return false
	}
	if normalizeTriageOrder(m.triageOrder) == triageOrderQuestionsOnly {
		return m.triageStateOf(sess) == status.Waiting
	}
	return true
}

// orderTriageKey reshapes a walkable session's key for the chosen order.
// Muted and resting rows keep theirs: the order decides what the drain hands
// over first, not what it hands over at all.
func (m *Model) orderTriageKey(key triageKey, sess store.Session) triageKey {
	switch normalizeTriageOrder(m.triageOrder) {
	case triageOrderPriority:
		key.asking = false
	case triageOrderOldest:
		key.asking, key.priority, key.tier = false, 0, 0
		key.band = triageBandScored
		// Oldest first is the lowest recency first, and the key sorts the
		// highest score first.
		key.score = -triageScore(sess)
	}
	return key
}
