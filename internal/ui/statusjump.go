package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A status jump is one key for "take me into the next session that is
// finished", and one for each of the other states a row can be in.
//
// The board could already answer that, but only by rearranging itself: w
// narrows the list to what needs a person, i builds a queue out of it. Both
// rebuild the tree to answer a question that is about a single row, and both
// have to be undone afterwards to get the tree back. A jump leaves the list
// exactly as it is and walks it.
//
// It enters the session it lands on rather than parking the cursor there,
// which is what makes it one gesture repeated: alt+f, deal with that one,
// ctrl+q, alt+f. A cursor move would need a second key on every row.
type statusJump struct {
	// label names the jump in the words the status column uses, for the
	// message left behind when there is nothing to walk to.
	label string
	// delta is the direction through the rows: +1 down, -1 up.
	delta int
	// wants is what this jump is looking for. See needsPerson.
	wants func(*Model, store.Session) bool
	// skipMuted holds the attention walk to the queue triage would hand
	// over: a muted row is one the operator has already said is not theirs
	// this pass, and tab is that queue. A jump that names a state answers
	// for the state -- somebody asking for the next finished session is
	// asking about the mark on the row, not about their queue.
	skipMuted bool
}

// statusJumps is the whole family, by action. The keys themselves live in
// the key map like every other binding, so a jump is rebindable and the
// defaults below are only where they start.
//
// Those defaults are alt+letter, not a plain letter: the list has a handful
// of unbound letters left and each one is what some later feature will want,
// while the modifier keeps the family mnemonic -- alt+w waiting, alt+f
// finished, alt+e errored -- instead of spelling five states out of whichever
// keys happened to be free. tab is the one a drain actually presses, so it
// gets the key that needs no modifier at all, and shift+tab walks back up.
var statusJumps = map[keymap.Action]statusJump{
	keymap.JumpAttention:     {label: "waiting on you", delta: 1, wants: (*Model).needsPerson, skipMuted: true},
	keymap.JumpAttentionBack: {label: "waiting on you", delta: -1, wants: (*Model).needsPerson, skipMuted: true},
	keymap.JumpWaiting:       {label: "waiting", delta: 1, wants: jumpStatus(status.Waiting)},
	keymap.JumpFinished:      {label: "finished", delta: 1, wants: jumpStatus(status.Finished)},
	// Errored and dead wear the same mark on the row, so one key answers for
	// both: a jump is named after what the operator can see, and the column
	// draws no difference between them. A dead pane cannot be entered, and
	// the walk carries on past it to the next one that can.
	keymap.JumpErrored: {label: "errored", delta: 1, wants: jumpStatus(status.Errored, status.Dead)},
	keymap.JumpIdle:    {label: "idle", delta: 1, wants: jumpStatus(status.Idle)},
	// Starting rides with working for the same reason: it is the first
	// second of a turn, and nobody looking for what is running means to skip
	// the session that has just been launched.
	keymap.JumpWorking: {label: "working", delta: 1, wants: jumpStatus(status.Working, status.Starting)},
}

// jumpStatus builds the predicate for a jump that is only about the mark on
// the row.
func jumpStatus(states ...string) func(*Model, store.Session) bool {
	return func(_ *Model, sess store.Session) bool {
		for _, state := range states {
			if sess.Status == state {
				return true
			}
		}
		return false
	}
}

// jumpToStatus walks to the next session this jump wants and enters it.
//
// A candidate that refuses to be entered -- a dead pane, a session killed
// between the poll and the key -- does not end the walk: the request was for
// the next session in that state that can be worked in, so the walk carries
// on and the refusal's own message is what stands if none can. tried is what
// keeps a refusal from being offered twice, the way an advance through the
// triage queue keeps one.
func (m *Model) jumpToStatus(jump statusJump) (tea.Model, tea.Cmd) {
	m.errBar.text = ""
	tried := map[string]bool{}
	for {
		index, ok := m.nextJumpRow(jump, tried)
		if !ok {
			break
		}
		tried[m.rows[index].sess.ID] = true
		if model, cmd, entered := m.enterJumpRow(index); entered {
			return model, cmd
		}
	}
	// Only once the rows on screen hold nothing: a fold is a browsing
	// convenience, and a jump that reported "no finished session" with one
	// sitting inside a folded group would be lying about the fleet rather
	// than about the view.
	if sess, ok := m.foldedJumpTarget(jump, tried); ok {
		if index, shown := m.revealSession(sess); shown {
			if model, cmd, entered := m.enterJumpRow(index); entered {
				return model, cmd
			}
		}
	}
	if m.errBar.text == "" {
		m.errBar.text = "nothing listed is " + jump.label
		return m, nil
	}
	// A walk that moved the cursor and then could not enter anything has
	// left the preview cleared behind it, and nothing else will fill it
	// until the next poll: the row the walk gave up on is the one the
	// operator is now looking at, so it is the one to capture.
	return m, m.schedulePreview()
}

// enterJumpRow puts the cursor on a row and focuses it, reporting whether the
// session took the focus. A refusal leaves the cursor on the row and the
// reason in the error bar, so a walk that never enters anything still ends
// somewhere that explains itself.
func (m *Model) enterJumpRow(index int) (tea.Model, tea.Cmd, bool) {
	m.cursor = index
	m.clearPreviewState()
	m.previewGen++
	_, cmd := m.focusSelected()
	if m.mode != modeFocus {
		return m, nil, false
	}
	return m, tea.Batch(cmd, m.schedulePreview()), true
}

// nextJumpRow is the row index this jump lands on, starting one step from the
// cursor and wrapping past the end of the list.
//
// The cursor's own row is the one place the walk never stops. Pressing alt+f
// on a finished session is a request for a different one, and the session
// just left still reads as finished for the poll or so it takes the pane to
// change, so honouring it would hand the operator straight back the row they
// just dealt with.
func (m *Model) nextJumpRow(jump statusJump, tried map[string]bool) (int, bool) {
	if len(m.rows) == 0 {
		return 0, false
	}
	start, _ := m.selectedIndex()
	for step := 1; step <= len(m.rows); step++ {
		index := (start + step*jump.delta) % len(m.rows)
		if index < 0 {
			index += len(m.rows)
		}
		row := m.rows[index]
		if !row.isSession() || index == start || tried[row.sess.ID] {
			continue
		}
		if m.jumpWants(jump, row.sess) {
			return index, true
		}
	}
	return 0, false
}

// jumpWants is one candidate's whole test, asked of a row on screen and of a
// session a fold is hiding alike.
func (m *Model) jumpWants(jump statusJump, sess store.Session) bool {
	if sess.Archived {
		// The archive is a filing cabinet: its rows cannot be focused at
		// all, so walking into one would only ever produce a refusal.
		return false
	}
	if jump.skipMuted && m.isMuted(sess) {
		return false
	}
	return jump.wants(m, sess)
}

// foldedJumpTarget is the first session this jump wants that has no row at
// all -- folded away inside a group, or under a parent whose children are
// folded.
//
// Not while a search is up: the query is a narrowing the operator typed, and
// a jump is not a request to leave it. Every other view that hides sessions
// -- the archive, the status filter, triage -- draws its rows unfolded, so
// there is nothing there for this pass to find and no case to special-case.
func (m *Model) foldedJumpTarget(jump statusJump, tried map[string]bool) (store.Session, bool) {
	if m.search != "" {
		return store.Session{}, false
	}
	shown := make(map[string]bool, len(m.rows))
	for _, row := range m.rows {
		if row.isSession() {
			shown[row.sess.ID] = true
		}
	}
	listed := store.OrderLinkedSessions(m.listedSessions())
	for _, sess := range floatBlocks(listed, m.sessions, floatedRoots(m.sessions)) {
		if shown[sess.ID] || tried[sess.ID] {
			continue
		}
		if m.jumpWants(jump, sess) {
			return sess, true
		}
	}
	return store.Session{}, false
}

// revealSession opens whatever is folded over a session -- the groups between
// it and root, the group it sits in, and its parent's children -- and returns
// its row once the tree has been rebuilt around it.
//
// The group's own fold is opened here where a jump to a group leaves it
// alone: arriving at a group is arriving at its heading, and arriving at a
// session means arriving at the row itself.
func (m *Model) revealSession(sess store.Session) (int, bool) {
	for path := sess.Group; path != rootGroup; path = parentGroup(path) {
		delete(m.collapsed, path)
	}
	if sess.ParentID != "" {
		m.setChildrenFolded(sess.ParentID, false)
	}
	m.rebuildRows()
	for index, row := range m.rows {
		if row.isSession() && row.sess.ID == sess.ID {
			return index, true
		}
	}
	// A retired member of a run draws as its run's fold line rather than as
	// a row, and no fold opens it. The walk treats that as one more
	// candidate it could not enter.
	return 0, false
}
