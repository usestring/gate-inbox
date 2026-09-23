package ui

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/sysstat"
)

// A board this size is mostly reached by scrolling, and a group four levels
// down is a dozen keystrokes away from one four levels up. So every group
// carries an outline number -- "2" for the second top-level group, "2.1" for
// its first child -- and typing that number puts the cursor on it.
//
// The number is written with separators but rarely typed with them: after a
// digit lands on a group, the next digit walks into that group's children, so
// "2.1" is reached by pressing 2 and then 1. The separator stays available for
// the boards where the two readings collide.
//
// The numbers are drawn from the tree the rail is showing rather than from
// the store, so what is typed always matches what is printed: a filter that
// takes a group out takes its number with it, and the ones left renumber.
// Folds are the exception, because a folded group's children keep their
// numbers and jumping to one opens the way down to it -- otherwise the keys
// would work only for the groups already on screen, which are the ones least
// in need of a shortcut.

// rootNumber is root's own number. Root is the group everything else hangs
// under and the standing spawn target, so it takes the digit before the
// first real group rather than a name that has to be typed.
const rootNumber = "0"

// groupJumpSettle is how long a partial number stays live. Long enough to
// reach for the second digit of "12" or the tail of "2.1", short enough that
// a number typed a minute later is read as a fresh one.
const groupJumpSettle = 1500 * time.Millisecond

// groupJump is the number typed so far. Empty means no jump is in progress.
type groupJump struct {
	buffer string
	gen    uint64
}

// groupJumpSettleMsg expires a partial number. gen tags it so a keystroke
// that extends the number leaves the older timer with nothing to expire.
type groupJumpSettleMsg struct{ gen uint64 }

// numberGroups walks the rendered tree in display order and gives every
// group its outline number.
func (m *Model) numberGroups(children map[string][]string) {
	numbers := map[string]string{rootGroup: rootNumber}
	byNumber := map[string]string{rootNumber: rootGroup}
	var walk func(parent, prefix string)
	walk = func(parent, prefix string) {
		for i, path := range children[parent] {
			number := prefix + strconv.Itoa(i+1)
			numbers[path] = number
			byNumber[number] = path
			walk(path, number+".")
		}
	}
	walk(rootGroup, "")
	m.groupNumbers, m.groupByNumber = numbers, byNumber
}

// clearGroupNumbers drops the numbering for a view that has no group rows.
// Triage and search flatten the tree into one queue of sessions, so there is
// nothing on screen a number could name.
func (m *Model) clearGroupNumbers() {
	m.groupNumbers, m.groupByNumber = nil, nil
	m.clearGroupJump()
}

// groupNumber is the outline number to print beside a group row, or "" when
// the view is not numbering groups.
func (m *Model) groupNumber(path string) string { return m.groupNumbers[path] }

// isGroupJumpKey reports the keys the jump reads. Digits are unbound
// everywhere else in the list, so nothing is taken over there. The separator
// is not: "." dismisses the row under the cursor, and keeps doing so, because
// no number begins with one -- it is only read as a separator once digits are
// already standing, which is also the only time it is needed.
func (m *Model) isGroupJumpKey(key string) bool {
	if key == "." {
		return m.jump.buffer != ""
	}
	return len(key) == 1 && key[0] >= '0' && key[0] <= '9'
}

// typeGroupNumber extends the number being typed and moves the cursor as
// soon as the digits name a group. "2" lands on the second group the moment
// it is pressed; a following "1" walks on into its first child, so the
// shortcut answers on every keystroke instead of waiting for a terminator.
func (m *Model) typeGroupNumber(key string) tea.Cmd {
	if len(m.groupByNumber) == 0 {
		m.errBar.text = "no groups in this view to jump to"
		return nil
	}
	candidate, ok := m.nextGroupNumber(key)
	if !ok {
		m.errBar.text = "no group numbered " + m.attemptedGroupNumber(key)
		m.clearGroupJump()
		return nil
	}
	m.errBar.text = ""
	m.jump.buffer = candidate
	m.jump.gen++
	if path, ok := m.groupByNumber[candidate]; ok {
		m.jumpToGroup(path)
	}
	return m.scheduleGroupJump()
}

// nextGroupNumber reads one key against the number typed so far, and reports
// the number that leaves it standing.
//
// A digit is read as a step into the group just reached before it is read as
// a wider number at the same level, because that is what the keys are for: on
// a board with four groups at the top, pressing 1 then 1 means the first
// child of the first group, and a fourteenth group is not what the second
// press could have meant. A board that does have one keeps it -- a digit
// completing a number some group actually carries wins over the step down --
// and the children underneath it are still reached through the separator, so
// no group is ever left with no way to type it.
func (m *Model) nextGroupNumber(key string) (string, bool) {
	buffer := m.jump.buffer
	if key == "." {
		if m.namesGroup(buffer) && m.groupNumberPrefix(buffer+".") {
			return buffer + ".", true
		}
		return "", false
	}
	if m.namesGroup(buffer + key) {
		return buffer + key, true
	}
	if m.namesGroup(buffer) && m.groupNumberPrefix(buffer+"."+key) {
		return buffer + "." + key, true
	}
	if m.groupNumberPrefix(buffer + key) {
		return buffer + key, true
	}
	// A digit that can extend nothing standing starts a fresh number, so a
	// finished jump never swallows the next one: 1 then 3 is the third group,
	// not a group 13 that does not exist.
	if m.groupNumberPrefix(key) {
		return key, true
	}
	return "", false
}

// attemptedGroupNumber is the number a refused key was reaching for, which is
// the step down whenever a step down was on offer. Reporting the digits
// concatenated instead would name a group at the wrong level.
func (m *Model) attemptedGroupNumber(key string) string {
	if key != "." && m.namesGroup(m.jump.buffer) {
		return m.jump.buffer + "." + key
	}
	return m.jump.buffer + key
}

// namesGroup reports whether a number is one a group actually carries, as
// against one still being typed towards.
func (m *Model) namesGroup(number string) bool {
	_, ok := m.groupByNumber[number]
	return ok
}

// groupNumberPrefix reports whether any group's number begins with the
// digits typed so far. It is what tells a number still being typed from one
// that cannot be finished.
func (m *Model) groupNumberPrefix(digits string) bool {
	for number := range m.groupByNumber {
		if strings.HasPrefix(number, digits) {
			return true
		}
	}
	return false
}

// scheduleGroupJump arms the expiry for the number now being typed.
func (m *Model) scheduleGroupJump() tea.Cmd {
	gen := m.jump.gen
	return tea.Tick(groupJumpSettle, func(time.Time) tea.Msg {
		return groupJumpSettleMsg{gen: gen}
	})
}

// clearGroupJump drops a partial number. Every other list key ends the jump:
// a number is a short burst of digits, and one left standing would silently
// swallow the next digit typed. The generation moves with it, so the timer
// the dropped number armed expires nothing when it lands.
func (m *Model) clearGroupJump() {
	m.jump = groupJump{gen: m.jump.gen + 1}
}

// jumpToGroup puts the cursor on a group, opening every fold between it and
// root first so the row the number named is actually on screen. The group's
// own fold is left alone: whether its sessions show is the operator's
// standing choice about that group, not part of arriving at it.
func (m *Model) jumpToGroup(path string) {
	for ancestor := parentGroup(path); ancestor != rootGroup; ancestor = parentGroup(ancestor) {
		delete(m.collapsed, ancestor)
	}
	m.rebuildRows()
	for i, entry := range m.rows {
		if entry.isGroup && entry.group == path {
			m.cursor = i
			m.preview = ""
			m.proc = sysstat.ProcStat{}
			m.procFor = ""
			return
		}
	}
	// A number is only minted for a group in the tree, so this is a filter
	// racing a keystroke rather than a number that was ever wrong.
	m.errBar.text = "group " + m.groupNumbers[path] + " is no longer listed"
}
