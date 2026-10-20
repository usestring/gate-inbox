package sessioncmd

import (
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/internal/store"
)

// A send to a child that has been filed away.
//
// The child sweep archives a finished child once its spawner has taken the
// finish in and left it for the grace window, ten minutes by default. Parents
// keep talking to their children for longer than that: a follow-up on the
// PR the child opened, a review comment to address. Of the 2,349
// send_session calls in one operator's transcripts, 160 failed and 106 of
// those were this refusal, every one followed by the same two calls the
// error named and the send again. Four more were a dead child the error
// sent to revive_session.
//
// A child is its spawner's to steer, and restoring and reviving it are calls
// that spawner may already make, so for that sender the send does them
// itself and says so. Anyone else still gets the refusal, now spelling out
// the exact calls with their arguments.

// restoreForSend brings targetID back onto the board and running when caller
// is the session tracking it and the send would otherwise be refused for it
// being archived or dead. It returns what it did, for the result, or "" when
// the target needed nothing or is not the caller's to restore; such a target
// is refused by deliverable as before.
//
// The relaunch comes before the restore, so a child whose directory or tool
// has gone stays filed where it was and the error says why.
func (s *Sessions) restoreForSend(runtime *runtime, caller store.Session, targetID string) (string, error) {
	if caller.ID == "" {
		return "", nil
	}
	target, err := runtime.agent(targetID)
	if err != nil || store.TrackerOf(target) != caller.ID {
		return "", nil
	}
	if err := runtime.reach(target); err != nil {
		return "", err
	}
	running := runtime.driver.Exists(target.ID)
	if !target.Archived && running {
		return "", nil
	}
	if !running {
		slot, err := s.pace(runtime, target.ID, true)
		if err == nil {
			_, err = s.relaunch(runtime, target, "", slot)
		}
		if err != nil {
			if target.Archived {
				return "", fmt.Errorf("session %s is your archived child, but it could not be revived to take this message: %w", target.ID, err)
			}
			return "", fmt.Errorf("session %s is your child and not running, and it could not be revived to take this message: %w", target.ID, err)
		}
	}
	if target.Archived {
		if err := runtime.store.SetArchived(target.ID, false); err != nil {
			return "", err
		}
	}
	switch {
	case target.Archived && !running:
		return fmt.Sprintf("restored archived child %s and revived it on its conversation", target.ID), nil
	case target.Archived:
		return fmt.Sprintf("restored archived child %s", target.ID), nil
	default:
		return fmt.Sprintf("revived child %s on its conversation", target.ID), nil
	}
}

// restoreCall is the exact call that restores id: the subcommand with the id
// after it for the shell front, the tool with its arguments for MCP.
func (w Vocabulary) restoreCall(id string) string {
	if strings.HasPrefix(w.Restore, cliCommand) {
		return w.Restore + " " + id
	}
	return fmt.Sprintf(`archive_session with {"session_id": %q, "archived": false}`, id)
}

// reviveCall is the exact call that revives id.
func (w Vocabulary) reviveCall(id string) string {
	if strings.HasPrefix(w.Revive, cliCommand) {
		return w.Revive + " " + id
	}
	return fmt.Sprintf(`revive_session with {"session_id": %q}`, id)
}
