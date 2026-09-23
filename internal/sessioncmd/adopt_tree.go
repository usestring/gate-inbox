package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/internal/logging"
)

// Repairing a fan-out that landed flat.
//
// A spawn records its caller as its parent, and when that fails there is
// nothing an agent can do about it. The failure is real: a session's MCP
// server is the build it started on, so a server older than the nesting path
// files every child it creates as a sibling, and the session doing the
// spawning cannot tell -- it asked to nest, the call succeeded, and the row
// came back looking like any other. Ten sessions were repaired by hand with a
// direct sqlite write the day this shipped, because the store knew how to
// re-parent a row and nothing exposed it.
//
// So: adopt takes a parentless row as a child, release lets one go. Confined
// the way the answer path is -- a session may claim what nobody owns and let
// go of what it owns, and may not take a row out of somebody else's tree.
// Rearranging another agent's fan-out is the board's job, and a person's.

// AdoptSession files targetID under the calling session.
//
// The store's own rules still apply on top of these: the tree carries one
// level, so a session that is itself a child cannot adopt, and a row with
// children of its own cannot become one.
func (s *Sessions) AdoptSession(sessionID, targetID string) (Session, error) {
	return s.place(sessionID, targetID, true)
}

// ReleaseSession takes one of the caller's own children back out to the top
// level, for a spawn that turned out to be its own piece of work rather than
// part of this one.
func (s *Sessions) ReleaseSession(sessionID, targetID string) (Session, error) {
	return s.place(sessionID, targetID, false)
}

func (s *Sessions) place(sessionID, targetID string, adopt bool) (Session, error) {
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return Session{}, err
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return Session{}, fmt.Errorf("session_id is empty; call %s to get one", runtime.words.ListSessions)
	}
	if targetID == caller.ID {
		return Session{}, errors.New("that is this session; a session cannot be its own parent or its own child")
	}
	target, err := runtime.store.Get(targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("session %s does not exist; call %s for current ids", targetID, runtime.words.ListSessions)
	}
	if err != nil {
		return Session{}, err
	}
	parentID := ""
	if adopt {
		// Somebody else's child stays theirs. The refusal names the owner
		// rather than the rule, because the caller's next move is to ask it.
		if target.ParentID != "" && target.ParentID != caller.ID {
			return Session{}, fmt.Errorf(
				"session %s is already filed under session %s; ask that session to release it first",
				target.ID, target.ParentID)
		}
		if target.ParentID == caller.ID {
			return runtime.sessionInfo(target, runtime.driver.Exists(target.ID), false), nil
		}
		parentID = caller.ID
	} else if target.ParentID != caller.ID {
		if target.ParentID == "" {
			return Session{}, fmt.Errorf("session %s is already top-level", target.ID)
		}
		return Session{}, fmt.Errorf(
			"session %s is filed under session %s, not under this one; only a parent releases its own children",
			target.ID, target.ParentID)
	}
	// A released row keeps the group it was drawn in rather than going to the
	// root: the group is where a person filed the work, and letting go of a
	// child is not a decision about that.
	group := target.Group
	if adopt {
		group = caller.Group
	}
	if err := runtime.store.PlaceSession(target.ID, group, parentID); err != nil {
		return Session{}, err
	}
	logging.Info("session tree repaired by an agent",
		"caller", caller.ID, "callerTool", caller.Tool,
		"session", target.ID, "parent", parentID)
	placed, err := runtime.store.Get(target.ID)
	if err != nil {
		return Session{}, err
	}
	return runtime.sessionInfo(placed, runtime.driver.Exists(placed.ID), false), nil
}

// FormatPlacement says where the row ended up, since the caller's reason for
// the call is that it did not end up there on its own.
func FormatPlacement(session Session) string {
	if session.ParentID == "" {
		return fmt.Sprintf("released %s (%s) to the top level of %s", session.Name, session.ID, groupLabel(session.Group))
	}
	return fmt.Sprintf("filed %s (%s) under session %s", session.Name, session.ID, session.ParentID)
}
