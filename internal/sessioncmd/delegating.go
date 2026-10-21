package sessioncmd

import (
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// delegated lays the delegating state over rows read from the store, the
// same overlay the board draws, so a tool never calls a parent finished
// while the board shows it still holding its fan-out.
func (r *runtime) delegated(sessions []store.Session) ([]store.Session, error) {
	pending, err := r.store.PendingChildReports()
	if err != nil {
		return nil, err
	}
	return store.ApplyDelegation(sessions, pending, func(sess store.Session) bool {
		return r.cfg.Tools[sess.Tool].Shell
	}), nil
}

// restingStatus is state as the tools report it for the session id: a
// finished or idle reading becomes delegating while the session still has
// children going, or a child's result not yet typed in. state is passed in
// rather than read from the row because read_session reads the live pane,
// which can be ahead of the last poll.
func (r *runtime) restingStatus(id, state string) string {
	if state != status.Finished && state != status.Idle {
		return state
	}
	sessions, err := r.store.ListSessions(true)
	if err != nil {
		logging.Info("could not read the board for delegation", "session", id, "error", err)
		return state
	}
	for i := range sessions {
		if sessions[i].ID == id {
			sessions[i].Status = state
		}
	}
	if sessions, err = r.delegated(sessions); err != nil {
		logging.Info("could not read pending child reports", "session", id, "error", err)
		return state
	}
	for _, sess := range sessions {
		if sess.ID == id {
			return sess.Status
		}
	}
	return state
}

// delegatingIDs is every session the overlay reads as delegating now.
func (r *runtime) delegatingIDs() (map[string]bool, error) {
	sessions, err := r.store.ListSessions(true)
	if err != nil {
		return nil, err
	}
	if sessions, err = r.delegated(sessions); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, sess := range sessions {
		if sess.Status == status.Delegating {
			ids[sess.ID] = true
		}
	}
	return ids, nil
}
