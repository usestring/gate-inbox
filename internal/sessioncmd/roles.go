package sessioncmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// What an extension's roles change here: which children a fan-out skips,
// which sends are the operator's words, and whose status an extension pins.
// The specs are the extensions' (see extension.RoleSpec); this package only
// reads the flags.

// skipsSendChildren is the reason send_children passes over child, or "".
func skipsSendChildren(child store.Session) string {
	if sessionhooks.Role(child.Role).SkipSendChildren {
		return "a helper the board launched for a role, not part of the fan-out"
	}
	return ""
}

// relayed reports whether caller's send to target is a relay: a session
// whose role relays to its parent, writing to that parent.
func relayed(caller, target store.Session) (extension.RoleSpec, bool) {
	if caller.ID == "" || caller.ParentID == "" || target.ID != caller.ParentID {
		return extension.RoleSpec{}, false
	}
	spec := sessionhooks.Role(caller.Role)
	return spec, spec.RelayToParent
}

// relay puts caller's message to its parent through the owning extension,
// and releases a pinned status once the relay is accepted: the session was
// pinned to hold something for the operator, and relaying is it letting go.
// It returns what to queue as the operator's words; "" queues nothing.
func (s *Sessions) relay(r *runtime, spec extension.RoleSpec, caller, target store.Session, message string) (string, error) {
	if caller.Archived {
		return "", errors.New("this session has been archived, so what it was relaying has been overtaken; nothing was sent")
	}
	hooksNow, err := sessionhooks.Current()
	if err != nil {
		return "", err
	}
	deliver, err := sessionhooks.Relay(hooksNow, caller, target, r.driver.Exists(caller.ID), r.driver.Exists(target.ID), message)
	if err != nil {
		return "", err
	}
	if spec.PinnedStatus {
		if err := s.releasePin(r, caller.ID); err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(deliver), nil
}

// releasePin removes a session's pinned status file and marks the row
// working, so the board stops showing what the pin held now rather than on
// its next pass, which then reads the pane again.
func (s *Sessions) releasePin(r *runtime, id string) error {
	if err := hooks.NewManager(s.configDir).Remove(id); err != nil {
		return err
	}
	return r.store.UpdateStatus(id, status.Working)
}

// PinnableStatus trims state and checks it is a status an extension may pin
// a session at: working, waiting, finished, idle or errored, or "" for none.
func PinnableStatus(state string) (string, error) {
	state = strings.TrimSpace(state)
	switch state {
	case "", status.Working, status.Waiting, status.Finished, status.Idle, status.Errored:
		return state, nil
	}
	return "", fmt.Errorf("%q is not a status to pin; pin one of working, waiting, finished, idle or errored, or \"\" to release", state)
}

// BoardPinStatus sets the status the board reads for a session an
// extension launched: owner is that extension's ID, and the session's role
// must be one of owner's whose spec pins its status. state "" releases the
// pin.
func (s *Sessions) BoardPinStatus(owner, targetID, state string) (err error) {
	defer start("sessioncmd.board.pin_status", sessionAttr(targetID)).done(&err)
	state, err = PinnableStatus(state)
	if err != nil {
		return err
	}
	runtime, err := s.open()
	if err != nil {
		return err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return err
	}
	if owner == "" || !strings.HasPrefix(target.Role, owner+"/") {
		return fmt.Errorf("session %s was not launched by extension %q for a role of its own, and the extension does not supervise it, so its status is not the extension's to pin", target.ID, owner)
	}
	if !sessionhooks.Role(target.Role).PinnedStatus {
		return fmt.Errorf("role %q does not pin its status; declare it with PinnedStatus", target.Role)
	}
	manager := hooks.NewManager(s.configDir)
	if state == "" {
		return manager.Remove(target.ID)
	}
	if err := manager.Write(target.ID, state); err != nil {
		return err
	}
	return runtime.store.UpdateStatus(target.ID, state)
}
