package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// childFoldPrefix namespaces a parent's child fold inside the tree's
// collapsed set, beside the group folds and the work folds already kept
// there. A group path is a slash-separated name, so the prefix cannot
// collide with one, and the set is where the promise to survive a restart is
// already kept.
const childFoldPrefix = "children:"

// childDepthCap is how deep a lineage draws before it flattens. The store
// allows one level of parenthood, so nothing reaches this today; it is here
// so that relaxing that rule cannot walk the list off the right edge.
const childDepthCap = 3

// deeperMark stands in for the levels a flattened row is really at, so a row
// drawn at the cap does not read as a direct child of the row above it.
const deeperMark = "…"

// childGlyph opens the child summary. U+21B3, not the U+2937 the design
// sketched: the glyph allow-list carries this one already, and the arrows
// block past U+21FF is exactly the sparsely-cut tail that list exists to
// keep out.
const childGlyph = "↳"

func (m *Model) childFoldDecision(parentID string) (folded, decided bool) {
	folded, decided = m.collapsed[childFoldPrefix+parentID]
	return folded, decided
}

func (m *Model) setChildrenFolded(parentID string, folded bool) {
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	m.collapsed[childFoldPrefix+parentID] = folded
}

func (m *Model) clearChildFold(parentID string) { delete(m.collapsed, childFoldPrefix+parentID) }

// childrenShown decides whether a parent's children are on screen. Children
// are folded until someone says otherwise: a fan-out of eight is eight rows
// of bloat between the parent and the next thing the human was reading, and
// the badge says what is down there. The cursor sitting on a child is the
// exception, so arriving on one from triage or a search opens its parent
// rather than selecting a row that is not drawn.
func (m *Model) childrenShown(parentID string) bool {
	if parentID == "" {
		return false
	}
	if folded, decided := m.childFoldDecision(parentID); decided {
		return !folded
	}
	return parentID == m.cursorParentID()
}

// cursorParentID is the parent of the session the tree is being built around,
// empty when that session is a root or there is no cursor.
func (m *Model) cursorParentID() string {
	if m.railCursorSess == "" {
		return ""
	}
	for _, sess := range m.sessions {
		if sess.ID == m.railCursorSess {
			return sess.ParentID
		}
	}
	return ""
}

// hasChildren reports whether a session is foldable at all. A terminal under
// a session is that session's own shell, opened deliberately with T and
// expected on screen; the fold is about spawned agents, so a session whose
// only children are shells has nothing to fold.
func (m *Model) hasChildren(sessID string) bool {
	if sessID == "" {
		return false
	}
	for _, sess := range m.sessions {
		if sess.ParentID == sessID && m.foldsAway(sess) {
			return true
		}
	}
	return false
}

// foldsAway reports whether a child is one the fold may take off screen. A
// shell is not: it is on screen because it was put there deliberately.
func (m *Model) foldsAway(child store.Session) bool {
	return !m.isShell(child.Tool)
}

// childStatusOrder is the order a badge reads in: what is running, then what
// is blocked on a person, then what is over. It is not the triage order,
// which ranks by who needs attention first; a badge is a census.
var childStatusOrder = []string{status.Working, status.Starting, status.Waiting, status.Finished, status.Errored, status.Dead}

// childCounts is a parent's live children by status, in the order above.
func (m *Model) childCounts(sessions []store.Session, parentID string) map[string]int {
	counts := map[string]int{}
	for _, sess := range sessions {
		if sess.ParentID == parentID && !sess.Archived && m.foldsAway(sess) {
			counts[sess.Status]++
		}
	}
	return counts
}

// childBadgeText is the summary a folded parent carries: how many children,
// then one count per status in the glyphs the child rows would have drawn
// themselves, then the archived ones in a trailing "(+N done)" so a fan-out
// that has been swept up still says it happened.
//
// statuses and archived are the two halves the ladder gives ground on; the
// count itself is never dropped, because a folded fan-out that says nothing
// at all is the one thing the tree must not do.
func (m *Model) childBadgeForm(sessions []store.Session, parentID string, archived int, statuses, done bool) string {
	counts := m.childCounts(sessions, parentID)
	live := 0
	for _, n := range counts {
		live += n
	}
	if live == 0 && archived == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d", childGlyph, live)
	if statuses {
		first := true
		for _, st := range childStatusOrder {
			n := counts[st]
			if n == 0 {
				continue
			}
			if first {
				b.WriteString(" · ")
				first = false
			} else {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%s%d", statusGlyph(st), n)
		}
	}
	if done && archived > 0 {
		fmt.Fprintf(&b, " (+%d done)", archived)
	}
	return b.String()
}

// childBadgeText is the widest form, which is what the tests read.
func (m *Model) childBadgeText(sessions []store.Session, parentID string, archived int) string {
	return m.childBadgeForm(sessions, parentID, archived, true, true)
}

// childBadgeLadder is the summary at each width, widest first. It gives up
// the per-status breakdown before the archived tally and the count last: the
// rows themselves carry the statuses once unfolded, while "how many are down
// there" is the only thing a folded parent can say at all.
func (m *Model) childBadgeLadder(parentID string) []string {
	archived := m.archivedChildren[parentID]
	forms := []string{
		m.childBadgeForm(m.sessions, parentID, archived, true, true),
		m.childBadgeForm(m.sessions, parentID, archived, true, false),
		m.childBadgeForm(m.sessions, parentID, archived, false, true),
		m.childBadgeForm(m.sessions, parentID, archived, false, false),
	}
	ladder := make([]string, 0, len(forms))
	seen := map[string]bool{}
	for _, form := range forms {
		if form == "" || seen[form] {
			continue
		}
		seen[form] = true
		ladder = append(ladder, form)
	}
	return ladder
}

// childBadge walks that ladder for the widest form the row has room for, and
// wears the same two-cell gap every other badge on the row does -- without
// it the badge before it ran straight into the arrow.
func (m *Model) childBadge(parentID string, room int) string {
	if !m.childCountsWorth(parentID) || room < badgeSeparator+1 {
		return ""
	}
	inner := room - badgeSeparator
	for _, text := range m.childBadgeLadder(parentID) {
		if lipgloss.Width(text) <= inner {
			return badgeGap + subtleText(text)
		}
	}
	return ""
}

// childCountsWorth keeps the badge off a row whose children are already
// drawn under it: the rows are the summary then, and a count of what the eye
// can see is noise.
func (m *Model) childCountsWorth(parentID string) bool {
	if m.childrenShown(parentID) {
		return m.archivedChildren[parentID] > 0
	}
	return true
}

// childFoldMark says a parent has children and whether they are showing, in
// the same vocabulary the group rows fold in.
func (m *Model) childFoldMark(sess store.Session) string {
	if !m.hasChildren(sess.ID) {
		return ""
	}
	if m.childrenShown(sess.ID) {
		return subtleText("▾")
	}
	return subtleText("▸")
}
