package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/internal/store"
)

// Mute sets or clears a session's persistent mute: the operator's "keep this
// out of my triage queue". Unlike the drain's ephemeral mark, which lapses
// as soon as the pane changes, this is stored on the row, so a muted session
// stays out of the queue across polls and restarts until it is unmuted.
//
// It reaches the same field the board's mute key writes, from the other
// side: an agent or a script marks a session without a manager running, and
// the manager's next poll picks the flag up like any other row change.
func (s *Sessions) Mute(sessionID, targetID string, muted bool) (mutedSession Session, err error) {
	defer start("sessioncmd.mute", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Session{}, err
	}
	target, err := runtime.mutable(targetID)
	if err != nil {
		return Session{}, err
	}
	if err := runtime.store.SetMuted(target.ID, muted); err != nil {
		return Session{}, err
	}
	target.Muted = muted
	return runtime.currentInfo(target, target.ID == sessionID), nil
}

// mutable resolves a session a mute may target. Any row on the board is
// fair game -- terminals included, since an idle shell is handed over by a
// drain like any other resting session.
func (r *runtime) mutable(id string) (store.Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.Session{}, fmt.Errorf("session_id is empty; call %s to get one", r.words.ListSessions)
	}
	sess, err := r.store.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, fmt.Errorf("session %s does not exist; call %s for current ids", id, r.words.ListSessions)
	}
	return sess, err
}
