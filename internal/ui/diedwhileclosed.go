package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A manager that starts up to find its panes gone -- the tmux server was
// restarted, or the box rebooted -- already holds everything needed to bring
// the fleet back. It never holds the launch to ask about them: each session
// that stopped without the operator ending it is marked on its row, the
// attention filter (w) keeps it, and V revives every dead row at once. The
// decision is the operator's, made from the list whenever they get to it.
//
// The check runs once, on the first pass whose statuses are real. A session
// that dies while the manager is up is ordinary attrition; it reads dead like
// any other and is not marked.

// diedMark is the agent run the startup pass marked, so a row revived and
// lost again is not still wearing the mark.
type diedMark struct {
	launched time.Time
}

// classifyDeadRows sorts the dead rows into the ones that stopped without the
// operator ending them, which it returns in list order, and the rest. A row
// the operator ended on purpose is never returned; see endclass.go.
func (m *Model) classifyDeadRows(ev endEvidence) ([]store.Session, endLedger) {
	out := make([]store.Session, 0, len(m.sessions))
	ends := endLedger{}
	for _, sess := range m.sessions {
		if sess.Archived || sess.Status != status.Dead {
			continue
		}
		class := classifyEnd(sess, ev)
		if live, running := m.supersededBy(sess); running {
			class = endClass{endSuperseded, "its conversation is running in " + live.Name}
		}
		ends[sess.ID] = class
		if class.verdict == endByOperator || class.verdict == endSuperseded {
			continue
		}
		out = append(out, sess)
	}
	return out, ends
}

// isDiedWhileClosed reports a row the startup pass marked, for as long as it
// is still the same dead agent run.
func (m *Model) isDiedWhileClosed(sess store.Session) bool {
	mark, ok := m.diedWhileClosed[sess.ID]
	return ok && !sess.Archived && sess.Status == status.Dead && mark.launched.Equal(sess.LaunchTime())
}

func (m *Model) markDied(sessions []store.Session) {
	if len(sessions) == 0 {
		return
	}
	m.diedWhileClosed = make(map[string]diedMark, len(sessions))
	for _, sess := range sessions {
		m.diedWhileClosed[sess.ID] = diedMark{launched: sess.LaunchTime()}
	}
}

// noteAdopted says what an adopt scan took. The panes are the takeover's
// now (takeover.go) unless outside panes are set to be kept as they are, in
// which case the line points at O instead.
func (m *Model) noteAdopted(msg adoptedMsg) {
	m.adoptFirstDone = true
	n := len(msg.ids)
	if n == 0 {
		return
	}
	switch m.outsidePanesMode() {
	case paneAdopt:
		m.reportDone(fmt.Sprintf("%d agent %s started outside the board added as-is; O takes %s over",
			n, plural(n, "pane", "panes"), plural(n, "it", "them")))
	case paneRelaunch:
		if m.restoreArmed {
			m.reportDone(fmt.Sprintf("%d agent %s started outside the board; taking %s over once idle",
				n, plural(n, "pane", "panes"), plural(n, "it", "them")))
		}
	}
}

// markDiedSessions runs once, on the first refresh that could see real
// statuses. Before the first poll every row reads dead, so running it at Init
// would mark a fleet that is already running.
//
// It never changes the screen. "mark" flags the rows and says so in one line;
// "resume" brings back the ones that clearly died and marks the unclear ones;
// "never" leaves them unmarked with a line pointing at V. Panes started outside the board are the
// takeover's unless the settings say to ignore them, which is applied here.
func (m *Model) markDiedSessions() {
	if !m.restoreArmed || m.restoreChecked {
		return
	}
	m.restoreChecked = true
	candidates, ends := m.classifyDeadRows(m.loadEndEvidence())
	var notices []string

	switch m.reopenSessionsMode() {
	case reopenResume:
		var died, unclear []store.Session
		for _, sess := range candidates {
			if ends[sess.ID].verdict == endDied {
				died = append(died, sess)
			} else {
				unclear = append(unclear, sess)
			}
		}
		m.markDied(unclear)
		if len(died) > 0 {
			m.reviveMany(died, "")
			notices = append(notices, fmt.Sprintf("resumed %d %s that died (settings: on reopen)",
				len(died), plural(len(died), "session", "sessions")))
		}
		if len(unclear) > 0 {
			notices = append(notices, fmt.Sprintf("%d with an unclear end marked %s, left for V", len(unclear), diedGlyph()))
		}
	case reopenNever:
		if len(candidates) > 0 {
			notices = append(notices, fmt.Sprintf("%d %s stopped without you ending them; V revives (settings: on reopen)",
				len(candidates), plural(len(candidates), "session", "sessions")))
		}
	default:
		if len(candidates) > 0 {
			m.markDied(candidates)
			notices = append(notices, fmt.Sprintf("%d %s stopped without you ending them, marked %s; V revives every dead session",
				len(candidates), plural(len(candidates), "session", "sessions"), diedGlyph()))
		}
	}

	if m.outsidePanesMode() == paneIgnore {
		if n := m.leaveOutPanes(m.adoptedCandidates()); n > 0 {
			notices = append(notices, fmt.Sprintf("%d outside %s left off the board (settings: outside panes)",
				n, plural(n, "pane", "panes")))
		}
	}

	if len(notices) > 0 {
		if m.errBar.text != "" && !m.errBar.worked() {
			m.errBar.text += "; " + strings.Join(notices, "; ")
			return
		}
		m.reportDone(strings.Join(notices, "; "))
	}
}
