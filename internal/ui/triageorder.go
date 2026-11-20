package ui

import (
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const (
	triageOrderPriority      = "priority-first"
	triageOrderOldest        = "oldest-first"
	triageOrderQuestionsOnly = "questions-only"
)

var triageOrders = []string{triageOrderPriority, triageOrderOldest, triageOrderQuestionsOnly}

const (
	triageOrderSetting = "triage_order"
	// triageKeepAlivesSetting keeps keep-alive rests in the drain. On by
	// default. The cache warden only keeps resting sessions warm, so every
	// keep-alive rest is a session that was idle or finished before the "ok",
	// and the drain would have handed it over without one. Leaving them out
	// took every idle session touched in the last hour out of the walk, which
	// are the ones whose context the operator still holds. A rest still ranks
	// below every real one (see triageChoreRest), and a mute the drain left
	// survives later keep-alives (see isMuted), so a warm session is not
	// handed back on each pass. Off leaves them on the rail only. See
	// chorerest.go.
	triageKeepAlivesSetting = "triage_keepalives"
	// triageKeepAlivesDefaultSetting records that a store has been through the
	// flip to on. saveSettings writes every field on every save, so a stored
	// "off" from before it is no evidence anybody chose it; the flip applies
	// once per store, and an "off" written after it stays. See
	// autoProceedDefaultSetting, which solved the same problem first.
	triageKeepAlivesDefaultSetting = "triage_keepalives_default_on"
)

func normalizeTriageOrder(order string) string {
	for _, known := range triageOrders {
		if known == order {
			return order
		}
	}
	// Existing boards that saved questions-first use the priority-led default.
	return triageOrderPriority
}

func storedTriageOrder(st *store.Store) string {
	chosen, err := st.Setting(triageOrderSetting)
	if err != nil {
		return triageOrderPriority
	}
	return normalizeTriageOrder(chosen)
}

func storedTriageKeepAlives(st *store.Store) bool {
	migrated, err := st.Setting(triageKeepAlivesDefaultSetting)
	if err != nil {
		return true
	}
	if migrated != autoProceedDefaultDone {
		if err := st.SetSetting(triageKeepAlivesSetting, "on"); err == nil {
			_ = st.SetSetting(triageKeepAlivesDefaultSetting, autoProceedDefaultDone)
		}
		return true
	}
	chosen, err := st.Setting(triageKeepAlivesSetting)
	return err != nil || chosen != "off"
}

// triageOrderLabel is the order as the rail's footer and Settings show it.
func triageOrderLabel(order string) string {
	switch normalizeTriageOrder(order) {
	case triageOrderOldest:
		return "priority then oldest"
	case triageOrderQuestionsOnly:
		return "priority · questions only"
	default:
		return "priority then questions"
	}
}

// inTriageRotation is whether the drain may hand sess over under the chosen
// order. It narrows the walk, never the rail: a session out of rotation
// keeps its row and its place, and opening it by hand works as before.
func (m *Model) inTriageRotation(sess store.Session) bool {
	if !m.inTriageContent(sess) {
		return false
	}
	if m.isChoreRest(sess) && !m.triageKeepAlives {
		return false
	}
	if normalizeTriageOrder(m.triageOrder) == triageOrderQuestionsOnly {
		return m.triageStateOf(sess) == status.Waiting
	}
	return true
}
