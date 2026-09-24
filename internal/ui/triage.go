package ui

import (
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// triageTiers ranks statuses by how badly a human is needed, best first.
// waiting outranks errored because a waiting agent is stalled on an answer
// this second, while an error has already happened and blocks no turn;
// working sits below every resting status because the one thing triage must
// never do is walk somebody into a session that is mid-turn.
var triageTiers = []string{
	status.Waiting,
	status.Errored,
	status.Finished,
	status.Idle,
	status.Working,
	status.Starting,
	status.Dead,
}

func triageRank(st string) int {
	for i, tier := range triageTiers {
		if tier == st {
			return i
		}
	}
	return len(triageTiers)
}

// triageRankOf is a session's tier.
func triageRankOf(sess store.Session) int {
	return triageRank(sess.Status)
}

// requiresInput is the one definition of "this session is waiting on a
// person": the sort, the auto-advance and the attention filter all read it,
// so the queue the rail paints and the queue ctrl+q walks cannot drift apart.
func requiresInput(st string) bool {
	switch st {
	case status.Waiting, status.Errored, status.Finished:
		return true
	}
	return false
}

// needsPerson is whether a session is on the operator's queue: the statuses
// requiresInput names. A method on the model so the status jumps can take it
// beside the walks that read more than a status.
func (m *Model) needsPerson(sess store.Session) bool {
	return requiresInput(sess.Status)
}

// triageWalkable is what a drain will hand over at all: the sessions that
// need a person, and behind them the idle ones. An idle session is not
// waiting on anybody, which is why it never counts as attention, but once
// the queue of sessions that are has been answered it is the next thing a
// drain can usefully put in front of the operator -- a session with nothing
// running is one that could be given work. Working, starting and dead stay
// off: entering a mid-turn session is the one thing triage must never do,
// and a dead pane cannot be entered.
//
// The rail paints this, the mute keys read it and the walk above filters on
// it, so all three say the same thing about a row.
func (m *Model) triageWalkable(sess store.Session) bool {
	return requiresInput(sess.Status) || sess.Status == status.Idle
}

// triageLess sorts by whether a person is needed, then the priority tier,
// then the status rank, then oldest first, so the session blocked longest is
// handed over first and the rail reads as a queue rather than as a ranking.
//
// The tier sits under the first key and over the status rank: an urgent
// session heads the sessions that need a person, or the rest, but never
// crosses from the second bucket into the first -- the drain hands over the
// sessions that need somebody before any idle one, urgent or not, and the
// rail has to read in the order the drain walks. See priority.go.
//
// LastStatusAt is when the session entered the state it is in, which is
// exactly how long it has been waiting; the two fields under it only keep
// the order total, so a redraw cannot reshuffle equal rows.
func (m *Model) triageLess(a, b store.Session) bool {
	if ka, kb := m.triageKeyOf(a), m.triageKeyOf(b); ka != kb {
		return ka.before(kb)
	}
	return triageLessWithin(a, b)
}

// triageKey is everything the queue orders by before it falls back to who has
// waited longest: whether somebody is needed at all, how much the work
// matters, and the status rank. It is a value rather than three comparisons
// in a row because a parent adopts the best key in its lineage, and "best"
// has to mean the same thing there as it does here.
//
// priority is the tier's rank rather than the tier itself, so an untiered
// session sorts with the mediums instead of ahead of or behind every stated
// tier at once. See priority.Rank.
type triageKey struct{ needs, priority, tier int }

func (k triageKey) before(o triageKey) bool {
	if k.needs != o.needs {
		return k.needs < o.needs
	}
	if k.priority != o.priority {
		return k.priority < o.priority
	}
	return k.tier < o.tier
}

// triageKeyOf reads one session's key. Zero sorts first in each field, so a
// session that needs a person and is urgent is {0, 0, tier}.
func (m *Model) triageKeyOf(sess store.Session) triageKey {
	key := triageKey{needs: 1, priority: m.tierOf(sess).Rank(), tier: triageRankOf(sess)}
	if m.needsPerson(sess) {
		key.needs = 0
	}
	return key
}

func (m *Model) sortTriage(sessions []store.Session) {
	m.sortTriageWithChildren(sessions, nil)
}

// sortTriageWithChildren is the queue sorted by the most urgent thing in each
// lineage. A child rides in under its parent rather than queueing on its own,
// so a parent still working while its child sits on a question would sink to
// the working tier and take the question down with it -- the one thing the
// queue must never do. The parent is lifted to its child's key instead, and
// the child is drawn directly under it, which is where the contract wants a
// child that needs a person: right after its parent.
func (m *Model) sortTriageWithChildren(sessions []store.Session, kids map[string][]store.Session) {
	now := time.Now()
	var live map[string]bool
	if len(kids) > 0 && m.tmux != nil {
		live = m.livePanes()
	}
	keys := make(map[string]triageKey, len(sessions))
	// A lifted parent is standing in for a child, so it queues on that
	// child's wait: ordering it by its own LastStatusAt would rank the
	// question by how long the parent has been working, which is not how
	// long anybody has been blocked.
	waited := make(map[string]store.Session, len(sessions))
	for _, sess := range sessions {
		best, holder := m.triageKeyOf(sess), sess
		for _, kid := range kids[sess.ID] {
			// A question this session is going to answer itself does not
			// lift it up the queue: the lift exists so a child blocked on a
			// person is not buried under its working parent, and a child
			// blocked on its parent is not blocked on a person. See
			// parentOwns.
			if m.parentOwns(kid, now, live) {
				continue
			}
			if k := m.triageKeyOf(kid); k.before(best) {
				best, holder = k, kid
			}
		}
		keys[sess.ID] = best
		waited[sess.ID] = holder
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		a, b := sessions[i], sessions[j]
		if ka, kb := keys[a.ID], keys[b.ID]; ka != kb {
			return ka.before(kb)
		}
		return triageLessWithin(waited[a.ID], waited[b.ID])
	})
}

// triageLessWithin breaks a tie inside a tier: oldest first, then the two
// fields that only keep the order total.
func triageLessWithin(a, b store.Session) bool {
	if !a.LastStatusAt.Equal(b.LastStatusAt) {
		return a.LastStatusAt.Before(b.LastStatusAt)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

const (
	triageSetting      = "triage_mode"
	triageScopeSetting = "triage_scope"
)

// storedTriage reads the persisted mode. Off is the default: triage
// rearranges the whole rail, so it is never something a user arrives at
// without having asked for it.
func storedTriage(st *store.Store) bool {
	chosen, err := st.Setting(triageSetting)
	if err != nil {
		return false
	}
	return chosen == "on"
}

// storedTriageScope reads the group the queue was last narrowed to. An unset
// or unreadable value is the whole fleet, which is what triage drained before
// it could be scoped at all.
func storedTriageScope(st *store.Store) string {
	scope, err := st.Setting(triageScopeSetting)
	if err != nil {
		return ""
	}
	return scope
}

// cursorTriageScope is the group the cursor is standing in when triage is
// asked for: the group row itself, or the group of the session under it.
// Root and the sessions filed loose under it are not a group, so they scope
// to the whole fleet -- a cursor outside every group is asking for all of
// them, which is the only reading that leaves the fleet-wide drain
// reachable.
func (m *Model) cursorTriageScope() string {
	entry, ok := m.selectedRow()
	if !ok {
		return ""
	}
	if entry.isGroup {
		return entry.group
	}
	return entry.sess.Group
}

// inTriageScope reports whether a group is inside the subtree triage was
// narrowed to. Subgroups come along with their parent: the scope is a
// subtree, not one level of it. An empty scope holds everything.
func (m *Model) inTriageScope(group string) bool {
	return m.triageScope == "" || inGroupSubtree(group, m.triageScope)
}

// persistTriage writes the mode from a command rather than from the key
// handler.
//
// The store keeps one connection, and a poll pass occupies it for as long as
// its own reads and writes take -- seconds, on a board this size. A write made
// on the event loop waits behind that, and the operator feels it as the key
// they pressed doing nothing: pressing "i" took most of a second.
//
// The mode is already flipped in the model by the time this runs, so the rail
// redraws immediately and the setting catches up. Losing the write is a
// setting that does not survive a restart, which is worth far less than a
// responsive key; the error still reaches the bar, and now actually stays
// there, since the old path set errBar.text and the caller cleared it on the
// next line.
func (m *Model) persistTriage() tea.Cmd {
	value := "off"
	if m.triage {
		value = "on"
	}
	scope := m.triageScope
	store := m.store
	// The scope is written first because it is only ever read through the
	// mode: a pair half-written by a failure leaves the group the queue
	// would have been narrowed to, under a mode that has not turned on yet.
	// The other order restores triage against whichever group the last
	// drain used.
	return deferStoreWrite(func() error {
		if err := store.SetSetting(triageScopeSetting, scope); err != nil {
			return err
		}
		return store.SetSetting(triageSetting, value)
	})
}

// toggleTriage flips the status-ordered queue, keeping the preview tied to
// the row the cursor lands on the way the other list toggles do.
func (m *Model) toggleTriage() tea.Cmd {
	previousKey, fromGroup := "", false
	if entry, ok := m.selectedRow(); ok {
		previousKey, fromGroup = rowKey(entry), entry.isGroup
	}
	m.triage = !m.triage
	if m.triage {
		// The queue is the group the cursor was in, read before the rebuild
		// flattens the groups away and takes the row that named it with
		// them. A drain is walked from inside one group far more often than
		// across the whole board, and a queue that hands over sessions from
		// work the operator is not doing is one they have to skip past.
		m.triageScope = m.cursorTriageScope()
		// Turning triage on is asking for that whole queue, from the top.
		// Marks left by an earlier drain would hide rows from a user who
		// has just said they want to see everything that needs them.
		m.clearMutes()
	} else {
		m.triageScope = ""
	}
	persist := m.persistTriage()
	m.errBar.text = ""
	m.rebuildRows()
	if m.triage {
		// Asking for the queue is asking for the work at the head of it, so
		// the drain opens inside that session rather than on a list the
		// operator then has to press enter on. A queue with nothing waiting
		// enters nothing, and the cursor rules below stand.
		if enter := m.enterTriageHead(); enter != nil {
			return tea.Batch(persist, enter)
		}
	}
	if m.triage && fromGroup {
		// The row that named the scope was a group row, and triage has
		// just flattened it away, so there is nothing for the cursor to be
		// restored onto and the index it kept indexes a tree that is gone.
		// The head of the queue is what the operator asked the group for,
		// found by scanning rather than assumed to be row zero, so this
		// keeps working whatever the queue comes to put above its first
		// session.
		for i, row := range m.rows {
			if row.isSession() {
				m.cursor = i
				break
			}
		}
	}
	return tea.Batch(persist, m.afterListFilter(previousKey))
}

// raisedTiers are the tiers that claim a pass of their own, highest first.
var raisedTiers = []priority.Tier{priority.Urgent, priority.High}

// nextTriageInput is the row index of the next session needing a person,
// starting after leftID in triage order and wrapping past the end.
//
// The session just left is skipped even while it still requires input:
// answering its question is what unfocused it, and the status it is left
// showing lags a poll behind, so honouring it would drop the user straight
// back into the session they just dealt with. Leaving it also muted it, which
// is what keeps it skipped for the rest of the drain rather than for this one
// hop; see mute.go. tried holds the candidates this advance already failed to
// enter, so a dead pane cannot be offered twice, and a leftID with no row
// left -- killed or archived while focused -- scans from the top rather
// than stranding the advance on a missing anchor.
//
// The walk is a pass per tier above the middle over the same ring -- urgent
// then high -- for the sessions that need a person, then a catch-all for the
// rest that do, then the same three passes over the idle ones. An idle
// session is never handed over ahead of one that is waiting, however the
// ring happens to be ordered around leftID, and a drain that has answered
// everything carries straight on into the idle sessions rather than dropping
// the operator on the list with work still to be given out.
//
// The raised tiers get their own passes rather than trusting the rail's
// order because the ring starts after leftID: an urgent session that came to
// need somebody while the drain was further down would otherwise wait for
// the ring to wrap, which is the one thing tiering it was meant to prevent.
// Medium and Low need no pass of their own -- the ring is walked in rail
// order, which already has them sorted, and a tier at or below the middle is
// not a claim on jumping the rotation.
func (m *Model) nextTriageInput(leftID string, tried map[string]bool) (int, bool) {
	start := -1
	for i, row := range m.rows {
		if row.isSession() && row.sess.ID == leftID {
			start = i
			break
		}
	}
	var passes []func(store.Session) bool
	for _, bucket := range []func(store.Session) bool{m.needsPerson, func(s store.Session) bool { return isIdle(s.Status) }} {
		for _, tier := range raisedTiers {
			passes = append(passes, func(s store.Session) bool { return bucket(s) && m.tierOf(s) == tier })
		}
		passes = append(passes, bucket)
	}
	for _, wanted := range passes {
		for step := 1; step <= len(m.rows); step++ {
			i := (start + step) % len(m.rows)
			row := m.rows[i]
			if !row.isSession() || row.sess.ID == leftID || tried[row.sess.ID] {
				continue
			}
			if row.sess.Archived || !wanted(row.sess) {
				continue
			}
			// A subagent is never the row a drain hands over. Its question
			// is its parent's to answer, and when nobody is coming for it
			// the parent is lifted to the child's key instead -- see
			// sortTriageWithChildren -- so the operator lands in the
			// session that spawned the work, not in the pane under it.
			if isSubagent(row.sess) {
				continue
			}
			// A muted session is one this drain has already handed over.
			// It still requires input on paper -- answering a question
			// does not clear the status until the poller sees the pane
			// change -- so without this the advance walks back up the
			// queue it just came down. An idle session stays idle after a
			// handover too, so the same mark is what moves the walk past
			// it. See mute.go.
			if m.isMuted(row.sess) {
				continue
			}
			// A pane that will not act on input cannot be answered, so
			// handing it over is handing the operator a session they can
			// only look at -- and it reads "waiting" for as long as it is
			// wedged, so the drain would return to it every pass. The mark
			// lapses the moment the pane paints anything. See deaf.go.
			if m.isDeaf(row.sess) {
				continue
			}
			return i, true
		}
	}
	return 0, false
}

func isIdle(st string) bool { return st == status.Idle }

// isSubagent is a session spawned under another one. A helper whose role
// keeps it on screen is not: it is there for the operator rather than for
// its parent, so the drain hands it over like a top-level session.
func isSubagent(sess store.Session) bool {
	return sess.ParentID != "" && !sessionhooks.Role(sess.Role).OnScreen
}

// enterTriageHead starts the queue at its head: the session that has been
// waiting longest and this pass has not already silenced.
//
// It is what a kill asks for, and what turning triage on asks for. Neither is
// a handover from a session the operator has just answered, so neither has a
// place in the queue to carry on from -- a killed row is gone, and a queue
// just switched on has never been walked. Advancing with no session left
// behind scans from the top, which is that head.
func (m *Model) enterTriageHead() tea.Cmd {
	return m.advanceTriage("")
}

// advanceTriage focuses the next session that needs a person. A nil command
// means there was none, and the caller's unfocus stands: the user lands back
// on the list rather than in some arbitrary session they never asked for.
func (m *Model) advanceTriage(leftID string) tea.Cmd {
	if len(m.rows) == 0 {
		return nil
	}
	tried := map[string]bool{}
	for {
		index, ok := m.nextTriageInput(leftID, tried)
		if !ok {
			return nil
		}
		next := m.rows[index].sess
		tried[next.ID] = true
		m.cursor = index
		m.clearPreviewState()
		m.previewGen++
		_, cmd := m.focusSelected()
		if m.mode == modeFocus {
			return tea.Batch(cmd, m.schedulePreview())
		}
	}
}
