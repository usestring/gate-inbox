package ui

import (
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A pane started outside the board is kept as it is: the board reads it,
// reaches it and answers for it without touching the process. Bringing it
// in -- ending it and resuming its conversation as a board session -- is
// the operator's call, offered two ways: a toast when the board adopts a
// pane while it runs, with one key to bring it in and one to leave it off
// the board, and the refusal of any action that needs the restart only a
// board session can have.

// adoptOfferFor is how long the toast for a newly adopted pane stays up,
// and with it the keys' claim on the panes it names. It outlives a status
// message's couple of polls because it asks for an answer.
const adoptOfferFor = 20 * time.Second

// adoptOffer is the panes the toast names, while it is up.
type adoptOffer struct {
	ids   []string
	text  string
	until time.Time
}

// errNeedsBringIn marks a refusal that bringing the pane in would lift.
var errNeedsBringIn = errors.New("needs the pane brought into the board")

// bringInRefusal is the refusal of an action an adopted pane cannot have
// until it is a board session, with the key that makes it one.
func (m *Model) bringInRefusal(sess store.Session, what string) error {
	return fmt.Errorf("%s runs in a pane you started, so the board cannot %s it; %s brings it in (resumes it as a board session once idle), then try again: %w",
		sess.Name, what, m.keyFor(keymap.ContextList, keymap.BringIn), errNeedsBringIn)
}

// keyFor is an action's first key as the operator reads it, or quick
// actions when it has none.
func (m *Model) keyFor(context keymap.Context, action keymap.Action) string {
	if m.km().Bound(context, action) {
		return m.cap(context, action)
	}
	return "quick actions (:)"
}

// offerAdopted puts the toast up for panes adopted while the board runs.
func (m *Model) offerAdopted(ids []string, now time.Time) {
	n := len(ids)
	if n == 0 {
		return
	}
	subject := fmt.Sprintf("%d agent panes started outside the board are on it as-is", n)
	if n == 1 {
		subject = "an agent pane started outside the board is on it as-is"
		// The scan's rows reach the board on the next refresh, so the name
		// is read from the store the scan wrote it to.
		if m.store != nil {
			if sess, err := m.store.Get(ids[0]); err == nil {
				subject = sess.Name + ", started outside the board, is on it as-is"
			}
		}
	}
	text := fmt.Sprintf("%s · %s brings %s in · %s leaves %s out", subject,
		m.keyFor(keymap.ContextList, keymap.BringIn), plural(n, "it", "them"),
		m.keyFor(keymap.ContextList, keymap.LeaveOut), plural(n, "it", "them"))
	m.offer = adoptOffer{ids: ids, text: text, until: now.Add(adoptOfferFor)}
	m.reportDone(text)
}

// offerLive reports whether the toast is still up: nothing has replaced it
// and it has not run out.
func (m *Model) offerLive(now time.Time) bool {
	return len(m.offer.ids) > 0 && m.errBar.text == m.offer.text && now.Before(m.offer.until)
}

// adoptTargets is what a and o act on: the panes the toast names while it
// is up, otherwise the selected row when it is an adopted pane.
func (m *Model) adoptTargets(now time.Time) ([]store.Session, string) {
	if m.offerLive(now) {
		want := map[string]bool{}
		for _, id := range m.offer.ids {
			want[id] = true
		}
		var out []store.Session
		for _, sess := range m.adoptedCandidates() {
			if want[sess.ID] {
				out = append(out, sess)
			}
		}
		m.offer = adoptOffer{}
		if len(out) > 0 {
			return out, ""
		}
	}
	entry, ok := m.selectedRow()
	if !ok || entry.isGroup {
		return nil, "pick a pane started outside the board first"
	}
	if entry.sess.TmuxPaneID == "" {
		return nil, entry.sess.Name + " is already a board session"
	}
	if entry.sess.Archived || entry.sess.Status == status.Dead || m.isShell(entry.sess.Tool) {
		return nil, entry.sess.Name + " has no live agent to bring in"
	}
	return []store.Session{entry.sess}, ""
}

// bringIn is a: the panes are owed the takeover, which resumes each as a
// board session on its own conversation once it is idle and unattended.
func (m *Model) bringIn() (tea.Model, tea.Cmd) {
	targets, refusal := m.adoptTargets(time.Now())
	if refusal != "" {
		m.errBar.text = refusal
		return m, nil
	}
	for _, sess := range targets {
		if m.takeover.tried != nil {
			delete(m.takeover.tried, sess.ID)
		}
		m.oweTakeover(sess.ID)
	}
	m.errBar.text = ""
	m.reportTakeover(m.takeoverPass())
	return m, nil
}

// leaveOut is o: the panes go off the board, untouched, and the scan never
// takes them again.
func (m *Model) leaveOut() (tea.Model, tea.Cmd) {
	targets, refusal := m.adoptTargets(time.Now())
	if refusal != "" {
		m.errBar.text = refusal
		return m, nil
	}
	n := m.leaveOutPanes(targets)
	m.reportDone(fmt.Sprintf("left %d %s off the board, still running where %s",
		n, plural(n, "pane", "panes"), plural(n, "it is", "they are")))
	return m, nil
}
