package ui

import (
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
)

// The rail docks two panels under its list: the selected session's details
// (its facts and opening prompt) and the system meters (context, computer,
// subscription). Each heads itself with a row that folds it, by click or by
// key, down to that one row, so an operator who wants the whole list can have
// it. Folded panels stay folded across runs.
const (
	railFoldDetails = "details"
	railFoldSystem  = "system"
)

func railFoldSetting(section string) string { return "rail_fold_" + section }

func storedRailFolds(st *store.Store) map[string]bool {
	folds := map[string]bool{}
	for _, section := range []string{railFoldDetails, railFoldSystem} {
		if v, err := st.Setting(railFoldSetting(section)); err == nil && v == "1" {
			folds[section] = true
		}
	}
	return folds
}

func railFoldAction(section string) keymap.Action {
	if section == railFoldSystem {
		return keymap.FoldRailSystem
	}
	return keymap.FoldRailDetails
}

// toggleRailFold folds or unfolds one rail panel and remembers it.
func (m *Model) toggleRailFold(section string) {
	if m.railFolds == nil {
		m.railFolds = map[string]bool{}
	}
	m.railFolds[section] = !m.railFolds[section]
	value := ""
	if m.railFolds[section] {
		value = "1"
	}
	if m.store != nil {
		if err := m.store.SetSetting(railFoldSetting(section), value); err != nil {
			m.errBar.text = err.Error()
		}
	}
}

// railFoldHead is a panel's header row: the standard disclosure triangle,
// the panel's name, and the key that folds it at the right edge.
func (m *Model) railFoldHead(section string, width int) contentLine {
	glyph := "▾ "
	if m.railFolds[section] {
		glyph = "▸ "
	}
	lead := spaces(railInset) + subtleStyle.Render(glyph) + labelStyle.Render(section)
	key := ""
	context := keymap.ContextList
	if m.mode == modeFocus {
		context = keymap.ContextFocus
	}
	if k := m.cap(context, railFoldAction(section)); k != "" {
		key = subtleStyle.Render(k)
	}
	return contentLine{text: rowColumns(lead, key, max(width-2, 1)), fold: section}
}
