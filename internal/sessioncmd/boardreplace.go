package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// BoardReplace starts a fresh agent session in targetID's place on a board
// extension's behalf, and ends targetID: a restart that drops the
// conversation and keeps the seat. The new row takes the old one's parent,
// group, spawner and place in the list, and its tool, directory, model,
// account, name and role wherever opts leaves them empty. The messages
// still queued for the old one and its file reservations move across, and
// the old row is left dead with its last screen, as BoardKill leaves it.
//
// The swap is one store transaction with the pane swap inside it, so a poll
// pass reads either the old row alone or the new one beside a dead old one,
// and a failure anywhere leaves the old session running as it was.
//
// With hold, the swap waits for BoardCommitReplace instead: the fresh
// session is launched as a leaf under targetID, which runs on untouched,
// and BoardAbortReplace takes it away again. The hold is recorded in the
// store before the pane starts, so one a killed board never settled is
// aborted by AbortOrphanedHolds at the next start.
//
// rolePrefix is the replacing extension's own role prefix: a session
// wearing some other extension's role is refused.
func (s *Sessions) BoardReplace(targetID, rolePrefix string, opts BoardLaunchOptions, hold bool) (created Session, err error) {
	op := start("sessioncmd.board.replace", sessionAttr(targetID))
	defer func() {
		op.done(&err, tracing.Attr{Key: "created", Value: created.ID}, tracing.Attr{Key: "hold", Value: fmt.Sprint(hold)})
	}()
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	old, prepared, err := s.prepareReplace(runtime, targetID, rolePrefix, opts, false)
	if err != nil {
		return Session{}, err
	}
	sess := prepared.sess
	launched := false
	launchPane := func() error {
		if err := runtime.driver.Create(sess.ID, sess.Cwd, prepared.command, prepared.env, 0, 0); err != nil {
			return err
		}
		launched = true
		return nil
	}
	if hold {
		// A leaf under the old session while both run: its own row, never a
		// second one in the old seat, until the commit moves it there.
		sess.ParentID = old.ID
		err = runtime.store.RecordHold(store.HeldReplacement{FreshID: sess.ID, OldID: old.ID, Owner: strings.TrimSuffix(rolePrefix, "/")})
		if err == nil {
			if err = runtime.store.LaunchSessionLeaf(sess, launchPane); err != nil {
				defer func() {
					if clearErr := runtime.store.ClearHold(sess.ID); clearErr != nil {
						logging.Warn("could not clear a failed hold's record", "session", sess.ID, logging.Err(clearErr))
					}
				}()
			}
		}
	} else {
		var moved store.Replacement
		moved, err = s.takeSeat(runtime, old, func(snapshot string, retire func() error) (store.Replacement, error) {
			return runtime.store.ReplaceSession(sess, old.ID, snapshot, func() error {
				if err := launchPane(); err != nil {
					return err
				}
				return retire()
			})
		})
		logReplaced(old.ID, sess, moved, err)
	}
	if err != nil {
		if launched {
			_ = runtime.driver.Kill(sess.ID)
		}
		return Session{}, err
	}
	accounts.RecordLaunch(runtime.store, sess.ID, sess.Tool, sess.Account)
	sessionhooks.Spawned(prepared.hooks, sess, extension.SpawnByExtension)
	if stored, err := runtime.store.Get(sess.ID); err == nil {
		sess.Group, sess.ParentID = stored.Group, stored.ParentID
	}
	_ = runtime.driver.SetLabel(sess.ID, sessionLabel(sess.Group, sess.Name))
	return runtime.sessionInfo(sess, true, false), nil
}

// BoardCommitReplace finishes a replacement BoardReplace held: freshID
// takes targetID's seat, queued messages and file reservations, and
// targetID's pane is ended, its row left dead with its last screen, all as
// an unheld BoardReplace would have done it. A freshID that is dead, or
// whose pane is gone, is refused with extension.ErrReplacementNotRunning
// and nothing changes: targetID keeps running. Once the swap is committed
// it reports success, even when freshID cannot be read back afterwards.
func (s *Sessions) BoardCommitReplace(targetID, freshID string) (committed Session, err error) {
	defer start("sessioncmd.board.replace.commit", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	old, err := runtime.store.Get(targetID)
	if err != nil {
		return Session{}, err
	}
	if old.Archived {
		return Session{}, fmt.Errorf("session %s was archived during the hold; its replacement %s stays where it is", old.ID, freshID)
	}
	moved, err := s.takeSeat(runtime, old, func(snapshot string, retire func() error) (store.Replacement, error) {
		return runtime.store.CommitReplacement(freshID, old.ID, snapshot, func() bool { return runtime.driver.Exists(freshID) }, retire)
	})
	logReplaced(old.ID, store.Session{ID: freshID}, moved, err)
	if errors.Is(err, store.ErrReplacementNotRunning) {
		return Session{}, fmt.Errorf("session %s: %w", freshID, extension.ErrReplacementNotRunning)
	}
	if err != nil {
		return Session{}, err
	}
	// An error from here would read as a refused commit.
	fresh, err := runtime.store.Get(freshID)
	if err != nil {
		logging.Warn("could not read a committed replacement back", "session", freshID, logging.Err(err))
		fresh = store.Session{ID: freshID, Group: old.Group, ParentID: old.ParentID}
	}
	_ = runtime.driver.SetLabel(fresh.ID, sessionLabel(fresh.Group, fresh.Name))
	return runtime.sessionInfo(fresh, runtime.driver.Exists(fresh.ID), false), nil
}

// BoardAbortReplace takes back a replacement BoardReplace held: freshID's
// pane is ended and its row deleted, as a launch that failed leaves
// nothing, and targetID is not touched. A freshID no longer held under
// targetID is refused, so an abort can never delete a session that has
// since taken a seat.
func (s *Sessions) BoardAbortReplace(targetID, freshID string) (err error) {
	defer start("sessioncmd.board.replace.abort", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return err
	}
	defer runtime.store.Close()
	fresh, err := runtime.store.Get(freshID)
	if err != nil {
		return err
	}
	if fresh.ParentID != targetID {
		return fmt.Errorf("session %s: %w", freshID, store.ErrNotHeld)
	}
	if err := s.dropHeld(runtime, freshID, true); err != nil {
		return err
	}
	logging.Info("held replacement aborted", "from", targetID, "fresh", fresh.ID)
	return nil
}

// AbortOrphanedHolds aborts every held replacement a board left unsettled
// because it was killed mid-hold, and is for the board's start, before any
// extension runs: then no hold on record has an extension to settle it.
// Each fresh session's pane is ended and its row deleted, as
// BoardAbortReplace does; the session it would have replaced is not
// touched. A record whose fresh session has since been filed elsewhere is
// only dropped. It returns how many holds were aborted.
func (s *Sessions) AbortOrphanedHolds() (aborted int, err error) {
	op := start("sessioncmd.board.replace.orphans")
	defer func() { op.done(&err, tracing.Attr{Key: "aborted", Value: aborted}) }()
	runtime, err := s.open()
	if err != nil {
		return 0, err
	}
	defer runtime.store.Close()
	holds, err := runtime.store.HeldReplacements()
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, h := range holds {
		fresh, err := runtime.store.Get(h.FreshID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Killed between the record and the row: a pane may still
			// have started under the id.
			err = s.dropHeld(runtime, h.FreshID, false)
		case err != nil:
		case fresh.ParentID != h.OldID:
			err = runtime.store.ClearHold(h.FreshID)
			if err == nil {
				continue
			}
		default:
			err = s.dropHeld(runtime, h.FreshID, true)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("session %s held in %s's place: %w", h.FreshID, h.OldID, err))
			continue
		}
		aborted++
		logging.Info("held replacement left by an earlier board aborted", "from", h.OldID, "fresh", h.FreshID, "extension", h.Owner)
	}
	return aborted, errors.Join(errs...)
}

// dropHeld ends a held fresh session's pane, then deletes its row, which
// clears its hold record, or clears the record alone when there is no row.
func (s *Sessions) dropHeld(runtime *runtime, freshID string, hasRow bool) error {
	if runtime.driver.Exists(freshID) {
		if err := runtime.driver.Kill(freshID); err != nil {
			return err
		}
	}
	if !hasRow {
		return runtime.store.ClearHold(freshID)
	}
	if err := runtime.store.Delete(freshID); err != nil {
		return err
	}
	if err := hooks.NewManager(s.configDir).Remove(freshID); err != nil {
		logging.Warn("could not clear an aborted replacement's status file", "session", freshID, logging.Err(err))
	}
	return nil
}

// BoardLaunchPlan is what a board launch would start: the new session's id,
// and its pane's command and environment.
type BoardLaunchPlan struct {
	SessionID string
	Command   string
	Env       map[string]string
}

// BoardPlanReplace is what BoardReplace would launch in targetID's place,
// composed by the same code with nothing written: no row, no pane, no
// account turn or borrower, no hook file. The spawn policies and launch
// contributors are asked as for the replace itself. The id is minted for the
// plan alone; a replace that follows mints its own, which is the one
// difference between the two.
func (s *Sessions) BoardPlanReplace(targetID, rolePrefix string, opts BoardLaunchOptions) (plan BoardLaunchPlan, err error) {
	defer start("sessioncmd.board.plan_replace", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return BoardLaunchPlan{}, err
	}
	defer runtime.store.Close()
	_, prepared, err := s.prepareReplace(runtime, targetID, rolePrefix, opts, true)
	if err != nil {
		return BoardLaunchPlan{}, err
	}
	return BoardLaunchPlan{SessionID: prepared.sess.ID, Command: prepared.command, Env: prepared.env}, nil
}

// prepareReplace checks that targetID may be replaced and prepares the
// session that would take its place, rehearsed or for real.
func (s *Sessions) prepareReplace(runtime *runtime, targetID, rolePrefix string, opts BoardLaunchOptions, rehearse bool) (store.Session, boardLaunch, error) {
	if opts.ParentID != "" || opts.Group != "" {
		return store.Session{}, boardLaunch{}, fmt.Errorf("a replacement takes session %s's place; it cannot be filed elsewhere", targetID)
	}
	old, err := replaceable(runtime, targetID, rolePrefix)
	if err != nil {
		return store.Session{}, boardLaunch{}, err
	}
	toolName := strings.TrimSpace(opts.Tool)
	if toolName == "" {
		toolName = old.Tool
	}
	tool, known := runtime.cfg.Tools[toolName]
	if !known {
		return store.Session{}, boardLaunch{}, fmt.Errorf("tool %q is not configured; configured tools are %s", toolName, strings.Join(agentToolNames(runtime), ", "))
	}
	if tool.Shell {
		return store.Session{}, boardLaunch{}, fmt.Errorf("tool %q opens a shell, not an agent", toolName)
	}
	dir := old.Cwd
	if strings.TrimSpace(opts.Directory) != "" {
		dir = opts.Directory
	}
	if dir, err = resolveTerminalDirectory(dir); err != nil {
		return store.Session{}, boardLaunch{}, err
	}
	sess := store.Session{
		ID:         uuid.NewString()[:8],
		Name:       strings.ReplaceAll(strings.TrimSpace(opts.Name), "/", "-"),
		Tool:       toolName,
		Cwd:        dir,
		Status:     status.Starting,
		SpawnedBy:  store.SpawnerOf(old),
		Model:      strings.TrimSpace(opts.Model),
		NameSource: store.SourceUser,
		Role:       opts.Role,
		// Placement is decided by the store from the old row; these are for
		// the spawn policies, which see the row as it will be filed.
		Group:    old.Group,
		ParentID: old.ParentID,
	}
	if sess.Name == "" {
		sess.Name = old.Name
	}
	if sess.Role == "" {
		sess.Role = old.Role
	}
	// A model or account names something only the old session's tool knows.
	if toolName == old.Tool {
		if sess.Model == "" {
			sess.Model = old.Model
		}
		sess.Account = old.Account
	}
	prepared, err := s.prepareBoardLaunch(runtime, sess, tool, opts.Prompt, opts.Args, old.ID, rehearse)
	if err != nil {
		return store.Session{}, boardLaunch{}, err
	}
	return old, prepared, nil
}

// replaceable is targetID when a board extension with rolePrefix may
// replace it.
func replaceable(runtime *runtime, targetID, rolePrefix string) (store.Session, error) {
	old, err := runtime.agent(targetID)
	if err != nil {
		return store.Session{}, err
	}
	if old.Archived {
		return store.Session{}, fmt.Errorf("session %s is archived; unarchive it before replacing it", old.ID)
	}
	if old.Role != "" && !strings.HasPrefix(old.Role, rolePrefix) {
		return store.Session{}, fmt.Errorf("session %s plays %s, another extension's role; only that extension may replace it", old.ID, old.Role)
	}
	return old, nil
}

// takeSeat runs swap, the store write that moves a fresh session into
// old's seat, handing it the old pane's last screen and the retire that
// ends the old pane inside the write, then clears the old session's status
// file.
func (s *Sessions) takeSeat(runtime *runtime, old store.Session, swap func(snapshot string, retire func() error) (store.Replacement, error)) (store.Replacement, error) {
	oldRunning := runtime.driver.Exists(old.ID)
	snapshot := ""
	if oldRunning {
		snapshot, _ = runtime.driver.CapturePane(old.ID)
	}
	moved, err := swap(snapshot, func() error {
		if oldRunning {
			return runtime.driver.Kill(old.ID)
		}
		return nil
	})
	if err != nil {
		return store.Replacement{}, err
	}
	// The old agent died without its session-end hook, so its status file
	// would otherwise decide what a revive of it reads as.
	if err := hooks.NewManager(s.configDir).Remove(old.ID); err != nil {
		logging.Warn("could not clear a replaced session's status file", "session", old.ID, logging.Err(err))
	}
	return moved, nil
}

func logReplaced(oldID string, fresh store.Session, moved store.Replacement, err error) {
	if err != nil {
		return
	}
	logging.Info("session replaced by an extension",
		"from", oldID, "to", fresh.ID, "role", fresh.Role,
		"forwarded", moved.Forwarded, "reservations", moved.Reservations)
}
