// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/store"
)

func (m *Model) launchNewSession(sess store.Session, tool config.Tool, baseCommand string) error {
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	if sess.LastStatusAt.IsZero() {
		sess.LastStatusAt = sess.CreatedAt
	}
	// A terminal is no agent: the extensions have no say in it.
	shell := m.isShell(sess.Tool)
	var sessionHooks *extension.SessionHooks
	var contributed map[string]string
	if !shell {
		var err error
		reason := extension.LaunchSpawn
		if sess.MigrationID != "" {
			reason = extension.LaunchMigrate
			sessionHooks, err = sessionhooks.Current()
		} else {
			// Before the pane is built, so a refusal costs nothing to undo.
			sessionHooks, err = sessionhooks.CheckSpawn(sess, extension.SpawnByOperator)
		}
		if err != nil {
			return err
		}
		if contributed, err = sessionhooks.Env(sessionHooks, sess, reason, sess.MigrationID); err != nil {
			return err
		}
	}
	command, env, err := m.buildLaunch(sess.Tool, tool, baseCommand, sess.ID, sess.Model, sess.Account, contributed)
	if err != nil {
		return err
	}
	// A shell is a leaf, so T on a child agent's row nests under it rather
	// than being refused for depth.
	create := m.store.LaunchSession
	if shell {
		create = m.store.LaunchSessionLeaf
	}
	launched := false
	if err := create(sess, func() error {
		err := m.tmux.Create(sess.ID, sess.Cwd, command, env, m.previewPaneWidth(), m.previewPaneHeight())
		launched = err == nil
		return err
	}); err != nil {
		if launched {
			_ = m.tmux.Kill(sess.ID)
		}
		_ = m.hooks.Remove(sess.ID)
		return err
	}
	if !shell && sess.MigrationID != "" {
		if err := m.followMigration(sessionHooks, sess); err != nil {
			_ = m.tmux.Kill(sess.ID)
			_ = m.store.Delete(sess.ID)
			_ = m.hooks.Remove(sess.ID)
			return err
		}
	} else if !shell {
		sessionhooks.Spawned(sessionHooks, sess, extension.SpawnByOperator)
	}
	accounts.RecordLaunch(m.store, sess.ID, sess.Tool, sess.Account)
	labelErr := m.tmux.SetLabel(sess.ID, sessionLabel(sess.Group, sess.Name))
	if m.launched == nil {
		m.launched = map[string]time.Time{}
	}
	m.launched[sess.ID] = time.Now()
	m.sessions = append(m.sessions, sess)
	m.rebuildRows()
	return labelErr
}

// followMigration tells the extensions that sess carries on its source's
// conversation. One that cannot follow it undoes the migration.
func (m *Model) followMigration(sessionHooks *extension.SessionHooks, sess store.Session) error {
	source, err := m.store.Get(sess.MigrationID)
	if err != nil {
		return err
	}
	return sessionhooks.Migrated(sessionHooks, source, sess, m.tmux.Exists(source.ID))
}

// landInNewSession is what every "make me a session" key ends with: the
// cursor on the row it just made, and the keyboard already inside that
// session's pane.
//
// Creating a session and then leaving the operator in the list is a step they
// always took next -- find the row, press the focus key -- and the row is
// nearly always somewhere they were not looking, because a new session sorts
// where its group puts it rather than under the cursor. So both halves happen
// here: focusSession moves the cursor, and focusSelected reads that cursor.
//
// Focus is entered at once rather than waiting for the pane's first bytes. The
// pane exists the moment tmux Create returns and the launch script has already
// cleared the screen (#960), so what the operator watches is the agent
// booting in its own pane -- exactly the frames they would see had they been
// sitting there. Waiting for first paint would instead leave an
// indeterminate stretch where the list still owns the keyboard, and the list's
// keys are single letters that archive, delete and quit: a keystroke aimed at
// the new agent would act on the board instead. Landing now is what makes
// every key after the create go where it was aimed.
//
// A launch that failed never reaches here -- its caller reports the error and
// returns -- and this refuses on its own terms too: a row filtered off the
// tree leaves the cursor where it was, and a pane that is somehow already gone
// leaves the operator in the list with the reason in the error bar, never in a
// dead pane.
//
// Unconditional, with no setting behind it. The operator asked for it as the
// default, and the cost of it being wrong is one esc, which lands back on the
// list with the cursor already on the new row.
func (m *Model) landInNewSession(id string) (tea.Model, tea.Cmd) {
	// Rebuilt first because the callers reset the status filter after the
	// launch, and until the tree is rebuilt against that filter the new row
	// is not on it for the cursor to find.
	m.rebuildRows()
	// A spawn started from inside a gate keeps the operator's place in the
	// queue rather than landing in the session it just made. See gate.go.
	if m.returnToGate() {
		_, focus := m.focusSelected()
		return m, tea.Batch(focus, m.refreshCmd())
	}
	m.focusSession(id)
	_, focus := m.focusSelected()
	return m, tea.Batch(focus, m.refreshCmd())
}

// forgetLaunch drops a row from the pending set, so a session the user has
// since sent away is not carried back onto the tree by a poll that listed
// the store before it was launched.
func (m *Model) forgetLaunch(id string) {
	delete(m.launched, id)
}

// keepPendingLaunches carries over the rows this run spawned that the poll
// has not looked for yet. A poll lists its sessions and then spends the pass
// in tmux and ps calls, so the list the UI finally receives can predate a
// launch and would otherwise blink the new agent off screen. The first poll
// to list the store after a launch is the authority on it, whether it
// reports the row or its absence.
func (m *Model) keepPendingLaunches(polled []store.Session, listedAt time.Time) []store.Session {
	for id, at := range m.launched {
		if listedAt.After(at) {
			delete(m.launched, id)
			continue
		}
		if !sessionGone(polled, id) {
			continue
		}
		for _, sess := range m.sessions {
			if sess.ID == id {
				polled = append(polled, sess)
				break
			}
		}
	}
	return polled
}
