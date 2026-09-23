package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/store"
)

// A phone terminal is short as well as narrow, and shorter still with its
// keyboard up: a 54x27 client keeps 27 rows with the keyboard open and near
// fifty without. Every fixed row the frame spends is a row the list or the
// pane does not get, so below these thresholds the chrome gives way in tiers
// rather than the list falling to its three-row floor while the machine dock
// keeps nine.

const (
	// layoutSetting stores the operator's override: "auto" measures the
	// terminal, "desktop" never tightens, "mobile" always does, "board"
	// gives the whole width to the list.
	layoutSetting = "layout"
	layoutAuto    = "auto"
	layoutDesktop = "desktop"
	layoutMobile  = "mobile"
	// layoutBoard is the manager read as a list of everything running: no
	// preview column, so the names, groups, statuses and work marks get the
	// full width instead of thirty percent of it. The preview is not lost --
	// focusing a session still opens its pane, full width, and leaving it
	// comes back to the board. That is the same one-panel frame a terminal
	// too narrow for two columns already draws, asked for rather than
	// measured.
	layoutBoard = "board"

	// tightHeight is the tallest terminal that still counts as short. It is
	// set by the device rather than by the arithmetic: the phone client this
	// exists for is 27 rows with its keyboard up, so a threshold at 22 would
	// never fire on the machine that needs it. The compact focus layout uses
	// the same number.
	tightHeight = 30
	// legendOneRowHeight and legendHiddenHeight are the legend's tiers: one
	// row below the first, none below the second.
	legendOneRowHeight = tightHeight
	legendHiddenHeight = 14
	// dockFullMinList is the fewest list rows the full machine dock may leave
	// standing; under it the dock drops to one line, and under railListMin
	// it goes entirely.
	dockFullMinList = 8
)

// layoutModes is the setting's cycle order.
var layoutModes = []string{layoutAuto, layoutDesktop, layoutMobile, layoutBoard}

// storedLayout reads the persisted layout override. Anything but a known
// value is auto, which is also what a fresh install gets.
func storedLayout(st *store.Store) string {
	chosen, err := st.Setting(layoutSetting)
	if err != nil {
		return layoutAuto
	}
	return normalizeLayout(chosen)
}

func normalizeLayout(chosen string) string {
	for _, mode := range layoutModes {
		if chosen == mode {
			return mode
		}
	}
	return layoutAuto
}

// toggleRail hides the list beside the pane, or brings back the layout it
// was hidden from. It is the layout setting's "board" under a key, and it
// reads the same way toggleChrome does one panel down: the same persisted
// setting the settings screen cycles, so a rail put away with the key is
// still away after a restart, but two states rather than four, because
// stepping through auto/desktop/mobile to get the columns back is three
// presses too many.
//
// The mode it came from is kept so that an operator who asked for the tight
// layout ("mobile") does not get the width tiers back instead when they
// bring the rail out of hiding.
func (m *Model) toggleRail() tea.Cmd {
	if m.layout == layoutBoard {
		m.layout = normalizeLayout(m.layoutShown)
	} else {
		m.layoutShown = m.layout
		m.layout = layoutBoard
	}
	if err := m.store.SetSetting(layoutSetting, m.layout); err != nil {
		m.errBar.text = err.Error()
	}
	// The columns the rail gives up are the pane's, so tmux has to be told
	// the box moved, for the same reason toggleChrome says below.
	return m.resizeSessions()
}

// tight reports whether the frame is on a terminal too small for the full
// chrome: one panel wide, or short enough that the legend and dock would
// starve the list. The override settles it either way.
func (m *Model) tight() bool {
	switch m.layout {
	case layoutDesktop:
		return false
	case layoutMobile:
		return true
	}
	return m.width < minSplitWidth || m.height < tightHeight
}

// short reports whether the terminal is short enough that rows, not columns,
// are the scarce thing. Width alone does not make it so: a forty-column rail
// on a tall terminal is a layout this manager is used at on purpose.
func (m *Model) short() bool {
	switch m.layout {
	case layoutDesktop:
		return false
	case layoutMobile:
		return true
	}
	return m.height < tightHeight
}

// stackedRows reports whether entries paint their meta on a second line.
// The comfortable density asks for it; a short terminal overrides it, since
// two-line entries on a twelve-row phone screen leave room for three.
func (m *Model) stackedRows() bool {
	return m.comfortableRows && !m.short()
}

// legendRows is the footer's height budget for this terminal: the full
// legend, one row, or none. Focus mode never goes below one, because the row
// it keeps is the one naming the key that gets back to the manager.
//
// The chrome setting outranks the height tiers, and "never" outranks the
// focus floor with them: somebody who asked for no footer meant in focus
// mode too, and ? still answers with the whole key map from either place.
func (m *Model) legendRows() int {
	switch m.chrome {
	case chromeAlways:
		return legendMaxRows
	case chromeNever:
		return 0
	}
	rows := legendMaxRows
	switch {
	case m.layout == layoutDesktop:
	case m.height < legendHiddenHeight:
		rows = 0
	case m.height < legendOneRowHeight || m.layout == layoutMobile:
		rows = 1
	}
	if m.mode == modeFocus && rows < 1 {
		rows = 1
	}
	return rows
}

// dockTier is how much of the machine block a rail body of height rows can
// carry beside its list: full, one line, or none.
type dockTier int

const (
	dockNone dockTier = iota
	dockBrief
	dockFull
)

// dockTierFor picks the tier from the rows the full dock would leave the
// list: the full dock keeps at least dockFullMinList rows and half the rail;
// the one-line dock keeps the list above its floor; otherwise there is no
// dock and the header carries the reading.
func dockTierFor(height, fullLines int) dockTier {
	const railListMin = 3
	if full := height - fullLines - 1; full >= dockFullMinList && full*2 >= height {
		return dockFull
	}
	if height-2 >= railListMin {
		return dockBrief
	}
	return dockNone
}

// dockShown reports whether any machine dock is on screen, which is what
// decides whether the header carries the cpu and memory reading instead. A
// one-panel terminal in focus mode draws no rail, so no dock. The meters are
// asked for at the rail's own width so the memo behind them stays warm.
func (m *Model) dockShown() bool {
	left, right := m.splitWidths()
	railWidth := left - 1
	if right == 0 {
		if m.mode == modeFocus {
			return false
		}
		railWidth = m.width - 1
	}
	return dockTierFor(m.listBodyHeight(), len(m.computerLines(railWidth))) != dockNone
}

// The chrome setting is the operator's answer to a frame that spends rows
// restating itself. The legend is reference material somebody who uses the
// manager daily has already learned. auto is what the height tiers above
// decide; always and never take the decision off the terminal and give it
// to the person.
const (
	chromeSetting = "chrome"
	chromeAuto    = "auto"
	chromeAlways  = "always"
	chromeNever   = "never"
)

// chromeModes is the setting's cycle order.
var chromeModes = []string{chromeAuto, chromeAlways, chromeNever}

func storedChrome(st *store.Store) string {
	chosen, err := st.Setting(chromeSetting)
	if err != nil {
		return chromeAuto
	}
	return normalizeChrome(chosen)
}

func normalizeChrome(chosen string) string {
	for _, mode := range chromeModes {
		if chosen == mode {
			return mode
		}
	}
	return chromeAuto
}

// toggleChrome hides the footer, or brings back the mode it was hidden from.
// It is the same persisted setting the settings screen cycles -- a footer put
// away with the key is still away after a restart -- but it is two states
// rather than three: somebody reaching for the key wants the rows back, and
// stepping through auto/always/never to get them is one press too many.
//
// The mode it came from is kept so that an operator who asked for the footer
// on a short terminal ("always") does not get the height tiers back instead
// when they bring it out of hiding.
func (m *Model) toggleChrome() tea.Cmd {
	if m.chrome == chromeNever {
		m.chrome = normalizeChrome(m.chromeShown)
	} else {
		m.chromeShown = m.chrome
		m.chrome = chromeNever
	}
	if err := m.store.SetSetting(chromeSetting, m.chrome); err != nil {
		m.errBar.text = err.Error()
	}
	// The rows the footer gives up are the pane's, so tmux has to be told
	// the box moved: a focused session that is not resized keeps drawing at
	// the old height, and the rows the key just freed stay blank until some
	// other event happens to resize it.
	return m.resizeSessions()
}
