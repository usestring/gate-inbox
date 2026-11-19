package ui

import (
	"cmp"
	"slices"

	"github.com/usestring/gate-inbox/internal/store"
)

const (
	listConsistencySetting     = "list_consistency"
	listConsistencyConditional = "conditional"
	listConsistencyPositional  = "positional"
)

func normalizeListConsistency(value string) string {
	if value == listConsistencyPositional {
		return value
	}
	return listConsistencyConditional
}

func storedListConsistency(st *store.Store) string {
	value, _ := st.Setting(listConsistencySetting)
	return normalizeListConsistency(value)
}

func (m *Model) keepListPositions(groups map[string][]store.Session) {
	mode := normalizeListSort(m.listSort)
	if normalizeListConsistency(m.listConsistency) != listConsistencyPositional || mode == listSortManual {
		m.listPositions = nil
		return
	}
	if m.listPositions == nil || m.listPositionSort != mode {
		m.listPositions = map[string]map[string]int{}
		m.listPositionSort = mode
	}
	for group, sessions := range groups {
		positions := m.listPositions[group]
		if positions == nil {
			positions = map[string]int{}
			m.listPositions[group] = positions
		}
		for _, sess := range sessions {
			if _, exists := positions[sess.ID]; !exists {
				positions[sess.ID] = len(positions)
			}
		}
		slices.SortStableFunc(sessions, func(a, b store.Session) int {
			return cmp.Compare(positions[a.ID], positions[b.ID])
		})
	}
}
