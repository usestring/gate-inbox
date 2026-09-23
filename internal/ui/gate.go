package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/clipboard"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
)

// The gate is v1's inbox, which this manager could already do and had no key
// for. Draining a queue the way v1 drained one -- one session on screen,
// answer it or skip it, the next thing needing a person promotes itself --
// took three separate arrangements here: triage on, "triage auto proceed" on
// in settings, and the layout set to board so the rail stops taking a third
// of the width. Nobody finds that combination, and two thirds of it are
// settings, so anybody who did find it then had to put all three back by
// hand afterwards.
//
// So the combination gets a key, and arming it is one gesture rather than
// three. The pieces are the ones already here -- this file adds no queue, no
// ordering and no advance of its own -- and the mode's whole job is to turn
// them on together, add the two keys a one-at-a-time drain needs from inside
// a pane, and put the operator's own arrangement back when the queue runs
// dry or they walk out of it.
type gateMode struct {
	on   bool
	menu bool
	// The three things arming changed, so leaving can undo exactly them.
	// They live for the mode and are not persisted: a mode is not a
	// preference, and a gate that outlived the run would be a board an
	// operator cannot explain the state of.
	priorTriage bool
	priorScope  string
	priorLayout string
	// returnID is the session a spawn started from the gate departed from, so
	// the flow lands the operator back in the queue when it finishes or is
	// cancelled. Empty when no gate spawn is in flight.
	returnID string
}

var gateMenuKeys = []struct {
	key    string
	action keymap.Action
}{
	{".", keymap.Dismiss}, {"q", keymap.LeaveHard},
	{"x", keymap.Archive}, {"n", keymap.NewSession},
	{"y", keymap.CopySessionID}, {"l", keymap.LastPane}, {"o", keymap.Editor},
	{"home", keymap.PreviewTop}, {"end", keymap.PreviewBottom},
	{"pgup", keymap.PreviewPageUp}, {"pgdown", keymap.PreviewPageDown},
}

func gateMenuAction(key string) (keymap.Action, bool) {
	for _, binding := range gateMenuKeys {
		if binding.key == key {
			return binding.action, true
		}
	}
	return "", false
}

func (m *Model) gateCap(action keymap.Action) string {
	if m.gate.menu {
		for _, binding := range gateMenuKeys {
			if binding.action == action {
				return binding.key
			}
		}
	}
	return m.fullCap(keymap.ContextFocus, action)
}

// toggleGate arms the drain, or puts back what it found.
func (m *Model) toggleGate() tea.Cmd {
	if m.gate.on {
		return m.disarmGate()
	}
	return m.armGate()
}

// armGate turns on the queue, gives the pane the whole width, and enters the
// session at the head of it.
//
// The layout is changed before the head is entered rather than after, so the
// session opens full width instead of opening beside a rail that is then
// taken away underneath it. It is set directly rather than through the
// setting, because the operator has not asked for a different layout: they
// have asked for a mode that happens to need the columns, and leaving gives
// them their own layout back.
//
// A queue with nothing in it leaves the mode armed and says so. Disarming
// here instead would be tidier to read and worse to use: the operator would
// press the key, watch nothing happen, and have no way to tell an empty
// board from a key that does not work.
func (m *Model) armGate() tea.Cmd {
	m.gate = gateMode{on: true, menu: true, priorTriage: m.triage, priorScope: m.triageScope, priorLayout: m.layout}
	m.layout = layoutBoard
	var enter tea.Cmd
	if m.triage {
		// Triage is already on, so its queue and its scope are the ones the
		// operator built and the gate keeps them. What arming still owes
		// them is a pass from the top, which is what turning triage on
		// would have given: marks from an earlier drain would silently hide
		// rows from somebody who has just asked to see everything.
		m.clearMutes()
		m.errBar.text = ""
		m.rebuildRows()
		enter = m.enterTriageHead()
	} else {
		enter = m.toggleTriage()
	}
	if m.mode != modeFocus {
		m.errBar.text = "nothing in the queue is waiting on you or idle"
	}
	// The columns the rail gives up are the pane's, so tmux has to be told
	// the box moved: a session entered into the old geometry keeps drawing
	// at the old width.
	return tea.Batch(enter, m.resizeSessions())
}

// disarmGate puts the three arrangements back. It is the one way out, so
// every exit -- the key again, ctrl+\ from inside a session, the queue
// running dry -- leaves the board in the same state.
//
// The triage pair is persisted on the way out rather than on the way in:
// arming writes whatever toggleTriage writes, and this is the write that
// makes the stored pair agree with the board again.
func (m *Model) disarmGate() tea.Cmd {
	// The row the drain ends on is not always a row the restored view has:
	// a session the operator had folded away comes back inside its closed
	// group and the cursor lands on the group instead, leaving the pane the
	// drain was showing under a row that is not it. So leaving takes the
	// same guard the triage key takes, which is the other way this board
	// rebuilds a list out from under its own preview.
	previousKey := ""
	if entry, ok := m.selectedRow(); ok {
		previousKey = rowKey(entry)
	}
	prior := m.gate
	m.gate = gateMode{}
	m.layout = normalizeLayout(prior.priorLayout)
	m.triage = prior.priorTriage
	m.triageScope = prior.priorScope
	// The marks are this drain's memory of what it has already shown. The
	// drain is over, so a queue armed again later starts from the top.
	m.clearMutes()
	m.errBar.text = ""
	m.rebuildRows()
	return tea.Batch(m.persistTriage(), m.afterListFilter(previousKey), m.resizeSessions())
}

// dismissFocused is the list's dismiss key reached from inside the pane, and
// the skip a one-at-a-time drain is missing without it: today a session that
// wants nothing from the operator has to be left first and dismissed from
// the row afterwards, which is two gestures and a trip back to a list the
// gate is not showing.
//
// It is the list's key rather than a second reading of it -- a finished
// session is marked idle, anything else is muted -- and then the handover
// the answer keys already use, so a skip and an answer leave the queue in
// the same state and land in the same next session.
func (m *Model) dismissFocused(sess store.Session) tea.Cmd {
	_, cmd := m.dismissSelected()
	return tea.Batch(cmd, m.handOverFocused(sess))
}

// gateSpawn starts a new session from inside the gate without giving up the
// operator's place in the queue, which v1's gate view did and a drain needs.
// The spawn is the ordinary one; only its landing changes, because
// returnToGate sends the operator back to the session they were draining
// rather than into the row just made. A spawn outside the gate is untouched.
func (m *Model) gateSpawn() (tea.Model, tea.Cmd) {
	if m.gate.on {
		if sess, ok := m.selected(); ok {
			m.gate.returnID = sess.ID
		}
	}
	next, cmd := m.startNewSession()
	// A flow that never opened -- no CLIs enabled, a spawn refused before it
	// built a pane -- left focus on the gate, and the return it armed must
	// not sit there to hijack the next spawn the operator starts from the
	// list.
	if m.mode == modeFocus {
		m.gate.returnID = ""
	}
	return next, cmd
}

// returnToGate re-enters the session a gate spawn departed from, if one is
// pending, and reports whether it took the operator there. It is called from
// the spawn's own landing and from the two flows that can cancel it, so a
// spawn, a form esc and a picker esc all leave the drain where they found it.
func (m *Model) returnToGate() bool {
	id := m.gate.returnID
	if id == "" {
		return false
	}
	m.gate.returnID = ""
	m.rebuildRows()
	m.focusSession(id)
	return true
}

// cancelSpawnToGate is the way out of the new-session flow when it was opened
// from the gate. Leaving the flow must not also leave the drain, so it puts
// the operator back in the session they were answering when a gate spawn is
// what opened the flow, and does nothing special otherwise.
func (m *Model) cancelSpawnToGate() (tea.Model, tea.Cmd) {
	m.mode = modeList
	if !m.returnToGate() {
		return m, nil
	}
	_, focus := m.focusSelected()
	return m, focus
}

// gateBack reopens the session the drain last stepped past. It is the pair
// swap `l` uses, reached from inside the pane: the gate advances by muting and
// moving on, so coming back also lifts the mute, and a session skipped by
// mistake is on the queue again rather than merely on screen.
func (m *Model) gateBack() (tea.Model, tea.Cmd) {
	target := m.prevFocusID
	next, cmd := m.focusLastPane()
	if target != "" && m.focusedID == target {
		m.unmute(target)
	}
	return next, cmd
}

// copySessionID puts the agent's own conversation id on the clipboard, the id
// a `--resume` or a transcript path is built from. The manager's row id is not
// it: that names the pane, and the operator wants the conversation.
func (m *Model) copySessionID(sess store.Session) (tea.Model, tea.Cmd) {
	id := strings.TrimSpace(sess.AgentSessionID)
	if id == "" {
		m.errBar.text = m.displayName(sess) + " has no agent session id yet"
		return m, nil
	}
	return m, func() tea.Msg {
		if err := writeClipboard(id); err != nil {
			return sessionIDCopiedMsg{err: err}
		}
		return sessionIDCopiedMsg{id: id}
	}
}

// writeClipboard is the seam the copy-session-id key writes through, so a test
// can drive it without a real system clipboard.
var writeClipboard = clipboard.WriteText

// sessionIDCopiedMsg reports a finished clipboard write so the status line can
// confirm it, the way the pane's own copy selection does.
type sessionIDCopiedMsg struct {
	id  string
	err error
}
