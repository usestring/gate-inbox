package sessioncmd

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Clearing a finished fan-out in one call.
//
// A parent that spawned eight children and read their results has eight rows
// left under it, and archive_session one id at a time is eight calls to say
// one thing. The board files a finished child away on its own once the parent
// has taken the finish in (see ui/childsweep.go); this is the parent saying so
// itself, sooner, for the whole set.
//
// It reaches the caller's own fan-out and nothing else: the children it
// spawned, and through them whatever those spawned. Each child goes through
// the same archive archive_session uses, so its screen is kept, the row sits
// in the archived view for the retention window, and revive_session or the
// board's restore brings it back.

// DefaultCleanupStatuses is what a cleanup takes when the caller names none:
// the states in which a child has nothing in hand. Waiting is left out
// because a question on screen is work the parent has not answered, and
// errored because a failure is something to read before it is filed.
var DefaultCleanupStatuses = []string{status.Finished, status.Idle, status.Dead}

// CleanupOptions says which of the caller's children a cleanup takes.
type CleanupOptions struct {
	// Statuses narrows the children taken to these states. Empty is
	// DefaultCleanupStatuses.
	Statuses []string
	// All takes every child whatever its state, including one the caller
	// asked to keep and one whose own children are still working.
	All bool
	// DryRun reports what would be archived without archiving anything.
	DryRun bool
}

// ChildCleaned is one child's outcome.
type ChildCleaned struct {
	SessionID string `json:"session_id" jsonschema:"child session id"`
	Name      string `json:"name" jsonschema:"that session's name"`
	Status    string `json:"status" jsonschema:"the child's state when the cleanup read it"`
	Archived  bool   `json:"archived" jsonschema:"whether it was archived (or, on a dry run, would be)"`
	// Descendants are the sessions under this child that went with it.
	Descendants []string `json:"descendants,omitempty" jsonschema:"ids of the sessions this child spawned, at any depth, archived with it"`
	Skipped     string   `json:"skipped,omitempty" jsonschema:"why this child was left alone; empty when it was archived"`
}

// ChildCleanup is what one call did to the caller's fan-out.
type ChildCleanup struct {
	Archived int            `json:"archived" jsonschema:"how many children were archived, not counting their own descendants"`
	Skipped  int            `json:"skipped" jsonschema:"how many were left alone, with a reason on each"`
	DryRun   bool           `json:"dry_run,omitempty" jsonschema:"true when nothing was archived and the result is only the plan"`
	Children []ChildCleaned `json:"children" jsonschema:"per-child outcome, in board order"`
}

// CleanupChildren archives the caller's own children in the given states,
// each with everything under it.
//
// A child that cannot be filed is reported rather than failing the call, so
// one stuck pane does not leave the other seven on the list.
func (s *Sessions) CleanupChildren(sessionID string, opts CleanupOptions) (cleaned ChildCleanup, err error) {
	defer start("sessioncmd.cleanup_children", sessionAttr(sessionID)).done(&err)
	wanted, err := cleanupStates(opts)
	if err != nil {
		return ChildCleanup{}, err
	}
	runtime, err := s.open()
	if err != nil {
		return ChildCleanup{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return ChildCleanup{}, err
	}
	sessions, err := runtime.store.ListSessions(true)
	if err != nil {
		return ChildCleanup{}, err
	}
	result := ChildCleanup{DryRun: opts.DryRun}
	for _, child := range sessions {
		// spawned_by, as send_children reads it: a caller that is itself a
		// child has its spawns filed beside it, not under it.
		if store.SpawnerOf(child) != caller.ID || child.Archived {
			continue
		}
		// A terminal is the caller's own shell, which close_terminal ends.
		if runtime.cfg.Tools[child.Tool].Shell {
			continue
		}
		entry := ChildCleaned{SessionID: child.ID, Name: child.Name, Status: child.Status}
		below := liveDescendants(sessions, child.ID)
		entry.Skipped, err = runtime.cleanupRefusal(child, below, wanted, opts.All)
		if err != nil {
			return ChildCleanup{}, err
		}
		if entry.Skipped == "" {
			for _, kid := range below {
				entry.Descendants = append(entry.Descendants, kid.ID)
			}
			if !opts.DryRun {
				if err := s.fileTree(runtime, child, below, caller.ID); err != nil {
					entry.Skipped = err.Error()
				}
			}
		}
		if entry.Skipped == "" {
			entry.Archived = true
			result.Archived++
		} else {
			result.Skipped++
		}
		result.Children = append(result.Children, entry)
	}
	if len(result.Children) == 0 {
		return ChildCleanup{}, errors.New("this session has no children on the active list to clean up")
	}
	logging.Info("fan-out cleaned up by its parent",
		"caller", caller.ID, "archived", result.Archived, "skipped", result.Skipped, "dryRun", opts.DryRun)
	return result, nil
}

// cleanupStates is the set of states a cleanup takes, checked.
func cleanupStates(opts CleanupOptions) (map[string]bool, error) {
	states := opts.Statuses
	if len(states) == 0 {
		states = DefaultCleanupStatuses
	}
	known := []string{status.Starting, status.Working, status.Waiting, status.Finished, status.Idle, status.Errored, status.Dead}
	wanted := make(map[string]bool, len(states))
	for _, state := range states {
		state = strings.ToLower(strings.TrimSpace(state))
		if !slices.Contains(known, state) {
			return nil, fmt.Errorf("status %q is not a session state; use %s", state, strings.Join(known, ", "))
		}
		wanted[state] = true
	}
	return wanted, nil
}

// cleanupRefusal is why child stays, or empty when it goes.
func (r *runtime) cleanupRefusal(child store.Session, below []store.Session, wanted map[string]bool, all bool) (string, error) {
	if child.TmuxPaneID != "" {
		return "a pane the manager adopted rather than started; archive it by id if you mean to end it", nil
	}
	if all {
		return "", nil
	}
	if !wanted[child.Status] {
		return fmt.Sprintf("%s, which this cleanup does not take", child.Status), nil
	}
	keep, err := r.store.KeepChild(child.ID)
	if err != nil {
		return "", err
	}
	if keep {
		return "spawned with keep; pass all to archive it anyway", nil
	}
	var busy []string
	for _, kid := range below {
		if kid.Status != status.Dead && !wanted[kid.Status] && !r.cfg.Tools[kid.Tool].Shell {
			busy = append(busy, fmt.Sprintf("%s (%s)", kid.Name, kid.Status))
		}
	}
	if len(busy) > 0 {
		return "its own children are still going: " + strings.Join(busy, ", "), nil
	}
	return "", nil
}

// liveDescendants is everything under id still on the active list.
func liveDescendants(sessions []store.Session, id string) []store.Session {
	var out []store.Session
	for _, sess := range store.Descendants(sessions, id) {
		if !sess.Archived {
			out = append(out, sess)
		}
	}
	return out
}

// fileDescendants archives everything under target before target itself
// goes, so archiving a parent never strands its fan-out running under a row
// nobody is watching.
func (s *Sessions) fileDescendants(runtime *runtime, target store.Session, callerID string) error {
	sessions, err := runtime.store.ListSessions(true)
	if err != nil {
		return err
	}
	below := liveDescendants(sessions, target.ID)
	// A session archiving its own ancestor is not asking to end itself, or
	// what it is running.
	spared := map[string]bool{callerID: true}
	if slices.ContainsFunc(below, func(sess store.Session) bool { return sess.ID == callerID }) {
		for _, sess := range store.Descendants(sessions, callerID) {
			spared[sess.ID] = true
		}
	}
	kept := below[:0:0]
	for _, sess := range below {
		if !spared[sess.ID] {
			kept = append(kept, sess)
		}
	}
	return s.fileBelow(runtime, kept, callerID)
}

// fileTree archives below and then root.
func (s *Sessions) fileTree(runtime *runtime, root store.Session, below []store.Session, callerID string) error {
	if err := s.fileBelow(runtime, below, callerID); err != nil {
		return err
	}
	_, err := s.file(runtime, root, true)
	return err
}

// fileBelow archives below deepest first, so a failure partway leaves no
// live row under one already filed. The caller is spared if it is one of
// them.
func (s *Sessions) fileBelow(runtime *runtime, below []store.Session, callerID string) error {
	for i := len(below) - 1; i >= 0; i-- {
		if below[i].ID == callerID {
			continue
		}
		if _, err := s.file(runtime, below[i], true); err != nil {
			return fmt.Errorf("archive %s: %w", below[i].ID, err)
		}
	}
	return nil
}

// noteSpawnerRead records that the caller took in target's finish, when the
// caller is the session that spawned it. It is what lets the board file a
// finished child away without the rest notice having been delivered first.
// A failure is logged and dropped: the read itself succeeded, and a child
// left on the list a little longer is the whole cost.
func noteSpawnerRead(runtime *runtime, callerID string, target store.Session) {
	if store.SpawnerOf(target) != callerID {
		return
	}
	if err := runtime.store.NoteSpawnerRead(target.ID, time.Now()); err != nil {
		logging.Info("spawner read not recorded", "session", target.ID, logging.Err(err))
	}
}

// FormatChildCleanup leads with the count and names what stayed.
func FormatChildCleanup(cleaned ChildCleanup) string {
	verb := "archived"
	if cleaned.DryRun {
		verb = "would archive"
	}
	line := fmt.Sprintf("%s %d of %d children", verb, cleaned.Archived, len(cleaned.Children))
	for _, child := range cleaned.Children {
		switch {
		case child.Skipped != "":
			line += fmt.Sprintf("\n- %s (%s) left: %s", child.Name, child.SessionID, child.Skipped)
		case len(child.Descendants) > 0:
			line += fmt.Sprintf("\n- %s (%s) with %d under it", child.Name, child.SessionID, len(child.Descendants))
		}
	}
	if cleaned.Archived > 0 && !cleaned.DryRun {
		line += "\narchived rows stay restorable for 7 days; revive_session brings one back"
	}
	return line
}
