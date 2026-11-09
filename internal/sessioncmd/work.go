package sessioncmd

import (
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// AttachWork records item as the calling session's own work, the way an
// extension's tool reports a page it published for the session. The item is
// filed under the caller and no other session: a session cannot put work on
// another's row, any more than it can claim another's pull request.
func (s *Sessions) AttachWork(sessionID string, item store.SessionWork) (err error) {
	defer start("sessioncmd.attach_work", sessionAttr(sessionID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return err
	}
	return runtime.store.AttachSessionWork(sessionID, []store.SessionWork{item}, time.Now())
}
