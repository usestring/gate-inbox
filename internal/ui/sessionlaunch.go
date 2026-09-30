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
	return m.launchNewSessionWith(sess, tool, baseCommand, nil)
}

// launchNewSessionWith is launchNewSession for a spawn from the new-session
// form, carrying what the operator chose in the extensions' fields on it.
func (m *Model) launchNewSessionWith(sess store.Session, tool config.Tool, baseCommand string, form map[string]string) error {
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
		if contributed, err = sessionhooks.Env(sessionHooks, sess, reason, sess.MigrationID, form); err != nil {
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
		if launched {
			m.markFreshPane(sess.ID)
		}
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
	accounts.RecordLaunch(sess.ID, sess.Tool, sess.Account)
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

// goneMark is the list state this run most recently gave a row, and when
// it gave it. deleted means the row must not appear at all; otherwise
// archived is the value the row's Archived flag should carry.
type goneMark struct {
	at       time.Time
	archived bool
	deleted  bool
}

// markSession records the loaded-list state this run just gave a session,
// so stale polls predating the change are reconciled on arrival instead
// of undoing it for a frame.
func (m *Model) markSession(id string, mark goneMark) {
	if m.gone == nil {
		m.gone = map[string]goneMark{}
	}
	mark.at = time.Now()
	m.gone[id] = mark
}

// markGroup records the archive state this run just gave a group path,
// so stale polls predating the change are reconciled on arrival instead
// of undoing it for a frame.
func (m *Model) markGroup(path string, mark goneMark) {
	if m.goneGroups == nil {
		m.goneGroups = map[string]goneMark{}
	}
	mark.at = time.Now()
	m.goneGroups[path] = mark
}

// dropRecentlyRemoved reconciles the rows a poll lists against what this
// run has just done to them: without it, a pass in flight across a delete,
// an archive, or a restore delivers its pre-change listing afterwards and
// blinks the old state back for one more frame. A stale copy of a deleted
// row is dropped; a stale copy of an archive or restore has its flag
// corrected to what the store was just written to say. A listing that
// postdates every recorded change retires those records instead.
func (m *Model) dropRecentlyRemoved(polled []store.Session, listedAt time.Time) []store.Session {
	if len(m.gone) == 0 {
		return polled
	}
	kept := make([]store.Session, 0, len(polled))
	for _, sess := range polled {
		mark, known := m.gone[sess.ID]
		switch {
		case !known || listedAt.After(mark.at):
			if known {
				delete(m.gone, sess.ID)
			}
			kept = append(kept, sess)
		case mark.deleted:
		default:
			sess.Archived = mark.archived
			kept = append(kept, sess)
		}
	}
	for id, mark := range m.gone {
		if listedAt.After(mark.at) {
			delete(m.gone, id)
		}
	}
	return kept
}

// stripDeletedGroups reconciles a stale listing's group rows, metadata,
// and archive flags against what this run has just done: a deleted group's
// header cannot hang back onto the tree, and an archive or restore of a
// group keeps its flag until a newer listing confirms it. A listing that
// postdates a change retires its marker instead.
func stripDeletedGroups(msg *refreshMsg, gone map[string]goneMark) {
	removed := make(map[string]bool, len(gone))
	for path, mark := range gone {
		switch {
		case msg.listedAt.After(mark.at):
			delete(gone, path)
		case mark.deleted:
			removed[path] = true
		default:
			if msg.archivedGroups == nil {
				msg.archivedGroups = map[string]bool{}
			}
			msg.archivedGroups[path] = mark.archived
		}
	}
	if len(removed) == 0 {
		return
	}
	groups := make([]string, 0, len(msg.groups))
	for _, group := range msg.groups {
		if !removed[group] {
			groups = append(groups, group)
		}
	}
	msg.groups = groups
	for path := range removed {
		delete(msg.groupPaths, path)
		delete(msg.archivedGroups, path)
	}
}
