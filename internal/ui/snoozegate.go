package ui

import "github.com/usestring/gate-inbox/internal/keymap"

// Timed snooze is an experiment, off until the operator turns it on in
// Settings → Experimental features (store.SnoozeSetting). Its binding stays
// in the catalog, so keys.toml still names it and a rebind survives the
// switch, but while it is off the board offers it nowhere: the chord is not
// read as the action (so a focused agent gets ctrl+alt+z as it would any unbound
// key), and the key map, the quick actions and the legend leave it out.
//
// What the switch never does is strand a session: the poll lifts a snooze
// set while it was on whatever the switch says now (see poller.go), and a
// snoozed row still reads when it comes back.

// experimentHidden reports whether action belongs to an experiment that is
// off, so nothing on the board should offer it.
func (m *Model) experimentHidden(action keymap.Action) bool {
	return action == keymap.Snooze && !m.snoozeOn
}

// withoutHiddenExperiments drops the key map rows of experiments that are
// off.
func (m *Model) withoutHiddenExperiments(sections []helpSection) []helpSection {
	out := make([]helpSection, 0, len(sections))
	for _, section := range sections {
		rows := make([]helpRow, 0, len(section.rows))
		for _, row := range section.rows {
			if row.action != "" && m.experimentHidden(row.action) {
				continue
			}
			rows = append(rows, row)
		}
		section.rows = rows
		out = append(out, section)
	}
	return out
}
