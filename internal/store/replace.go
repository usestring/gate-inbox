package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Replacement is what ReplaceSession moved from the old row to the new one.
type Replacement struct {
	// Forwarded counts the undelivered messages re-addressed to the new row.
	Forwarded int64
	// Reservations counts the file leases the new row now holds.
	Reservations int64
}

// ErrNotHeld is a CommitReplacement whose new row is no longer held under
// the old one: somebody deleted, archived or moved it during the hold.
var ErrNotHeld = errors.New("the replacement is no longer held under the session it replaces")

// ErrReplacementNotRunning is a CommitReplacement whose new row is dead or
// whose pane is gone: committing would retire a running session for one
// that is not.
var ErrReplacementNotRunning = errors.New("the replacement is no longer running")

// ReplaceSession files sess in oldID's place and retires oldID, all in one
// transaction: the new row takes the old one's parent, group and place in
// the order, the old one's undelivered inbox and file leases move to it,
// and the old row is left dead with snapshot as its last screen.
//
// launch runs inside the transaction, after every write, and is where the
// caller swaps the panes: start the new one, end the old one. An error from
// it rolls every write back, so a reader of this store sees either the old
// row alone or the new row with the old one already dead -- never both
// alive. Like LaunchSession's, the callback must not use this Store.
func (s *Store) ReplaceSession(sess Session, oldID, snapshot string, launch func() error) (Replacement, error) {
	if sess.ID == "" || oldID == "" || sess.ID == oldID {
		return Replacement{}, fmt.Errorf("a replacement needs a new id distinct from %q", oldID)
	}
	if sess.MigrationID != "" {
		return Replacement{}, errors.New("a replacement is not a migration")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Replacement{}, err
	}
	defer tx.Rollback()
	at, err := seatOf(tx, oldID)
	if err != nil {
		return Replacement{}, err
	}
	sess.Group = at.group
	sess.ParentID = at.parentID
	if sess.SpawnedBy == "" {
		sess.SpawnedBy = at.parentID
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	if sess.LastStatusAt.IsZero() {
		sess.LastStatusAt = sess.CreatedAt
	}
	pendingInputs, err := encodePendingInputs(sess.PendingInputs)
	if err != nil {
		return Replacement{}, err
	}
	// Inserted at the old row's own order; takeSeat moves it straight after.
	if _, err := tx.Exec(
		`INSERT INTO sessions (id, name, tool, cwd, group_name, status, archived, created_at, last_status_at, agent_session_id, tmux_socket, tmux_pane_id, pending_inputs, parent_id, spawned_by, launch_prompt, model, account, name_source, priority_tier, role, sort_order)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.Name, sess.Tool, sess.Cwd, sess.Group, sess.Status,
		encodeTime(sess.CreatedAt), encodeTime(sess.LastStatusAt), sess.AgentSessionID,
		sess.TmuxSocket, sess.TmuxPaneID, pendingInputs, sess.ParentID, sess.SpawnedBy, sess.LaunchPrompt, sess.Model, sess.Account,
		nameSourceOr(sess.NameSource), string(sess.Priority), sess.Role, at.order,
	); err != nil {
		return Replacement{}, err
	}
	moved, err := takeSeat(tx, sess.ID, oldID, at, snapshot)
	if err != nil {
		return Replacement{}, err
	}
	if launch != nil {
		if err := launch(); err != nil {
			return Replacement{}, err
		}
	}
	return moved, tx.Commit()
}

// CommitReplacement finishes a held replacement: newID, filed under oldID
// while both ran, takes oldID's seat, inbox and leases exactly as
// ReplaceSession would have given them, and oldID is left dead with
// snapshot as its last screen. retire runs inside the transaction, after
// every write, and is where the caller ends the old pane; an error from it
// rolls every write back. It fails with ErrNotHeld when newID is no longer
// held under oldID, and with ErrReplacementNotRunning when newID's row is
// dead or running reports its pane gone; both are read inside the
// transaction, so nothing changes. The hold's record is cleared with the
// swap.
func (s *Store) CommitReplacement(newID, oldID, snapshot string, running func() bool, retire func() error) (Replacement, error) {
	if newID == "" || oldID == "" || newID == oldID {
		return Replacement{}, fmt.Errorf("a replacement needs a new id distinct from %q", oldID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Replacement{}, err
	}
	defer tx.Rollback()
	var heldBy, state string
	var archived int
	err = tx.QueryRow(`SELECT parent_id, archived, status FROM sessions WHERE id = ?`, newID).Scan(&heldBy, &archived, &state)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (heldBy != oldID || archived != 0) {
		return Replacement{}, fmt.Errorf("session %s: %w", newID, ErrNotHeld)
	}
	if err != nil {
		return Replacement{}, err
	}
	if state == "dead" || running != nil && !running() {
		return Replacement{}, fmt.Errorf("session %s: %w", newID, ErrReplacementNotRunning)
	}
	if _, err := tx.Exec(`DELETE FROM replace_holds WHERE fresh_id = ?`, newID); err != nil {
		return Replacement{}, err
	}
	at, err := seatOf(tx, oldID)
	if err != nil {
		return Replacement{}, err
	}
	moved, err := takeSeat(tx, newID, oldID, at, snapshot)
	if err != nil {
		return Replacement{}, err
	}
	if retire != nil {
		if err := retire(); err != nil {
			return Replacement{}, err
		}
	}
	return moved, tx.Commit()
}

// seat is where a row is filed: its group, its parent and its place among
// that parent's rows.
type seat struct {
	group, parentID string
	order           int64
}

func seatOf(tx *sql.Tx, id string) (seat, error) {
	var at seat
	err := tx.QueryRow(`SELECT group_name, parent_id, sort_order FROM sessions WHERE id = ?`, id).Scan(&at.group, &at.parentID, &at.order)
	if errors.Is(err, sql.ErrNoRows) {
		return seat{}, fmt.Errorf("session %s: %w", id, err)
	}
	return at, err
}

// takeSeat is the swap both kinds of replacement make: newID is filed
// directly after oldID, where the operator was already looking, oldID's
// undelivered inbox and file leases move to it, and oldID is left dead
// with newID recorded as what replaced it.
func takeSeat(tx *sql.Tx, newID, oldID string, at seat, snapshot string) (Replacement, error) {
	if _, err := tx.Exec(`UPDATE sessions SET sort_order = sort_order + 1
		 WHERE group_name = ? AND parent_id = ? AND sort_order > ? AND id != ?`, at.group, at.parentID, at.order, newID); err != nil {
		return Replacement{}, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET group_name = ?, parent_id = ?, sort_order = ? WHERE id = ?`,
		at.group, at.parentID, at.order+1, newID); err != nil {
		return Replacement{}, err
	}
	var moved Replacement
	res, err := tx.Exec(`
UPDATE session_inbox SET session_id = ?, claimed_at = 0
 WHERE session_id = ? AND delivered_at = 0 AND dropped_at = 0`, newID, oldID)
	if err != nil {
		return Replacement{}, err
	}
	if moved.Forwarded, err = res.RowsAffected(); err != nil {
		return Replacement{}, err
	}
	res, err = tx.Exec(`UPDATE file_reservations SET session_id = ? WHERE session_id = ?`, newID, oldID)
	if err != nil {
		return Replacement{}, err
	}
	if moved.Reservations, err = res.RowsAffected(); err != nil {
		return Replacement{}, err
	}
	now := encodeTime(time.Now())
	retire := `UPDATE sessions SET status = 'dead', last_status_at = ?, replaced_by = ? WHERE id = ?`
	args := []any{now, newID, oldID}
	if snapshot != "" {
		retire = `UPDATE sessions SET status = 'dead', last_status_at = ?, replaced_by = ?, snapshot = ? WHERE id = ?`
		args = []any{now, newID, snapshot, oldID}
	}
	if _, err := tx.Exec(retire, args...); err != nil {
		return Replacement{}, err
	}
	return moved, nil
}

// HeldReplacement is the record of a replacement an extension holds: the
// fresh session filed under the old one, and the extension that holds it.
type HeldReplacement struct {
	FreshID, OldID, Owner string
	Since                 time.Time
}

// RecordHold notes a held replacement before its fresh session exists, so
// a board killed while it is held -- before or after the pane starts --
// leaves a record the next start aborts. CommitReplacement and deleting the
// fresh row clear it; ClearHold clears one whose launch failed.
func (s *Store) RecordHold(h HeldReplacement) error {
	if h.FreshID == "" || h.OldID == "" {
		return errors.New("a held replacement needs both sessions' ids")
	}
	if h.Since.IsZero() {
		h.Since = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO replace_holds (fresh_id, old_id, owner, created_at) VALUES (?, ?, ?, ?)`,
		h.FreshID, h.OldID, h.Owner, encodeTime(h.Since))
	return err
}

// ClearHold drops freshID's hold record, if there is one.
func (s *Store) ClearHold(freshID string) error {
	_, err := s.db.Exec(`DELETE FROM replace_holds WHERE fresh_id = ?`, freshID)
	return err
}

// HeldReplacements is every hold still recorded, oldest first.
func (s *Store) HeldReplacements() ([]HeldReplacement, error) {
	rows, err := s.db.Query(`SELECT fresh_id, old_id, owner, created_at FROM replace_holds ORDER BY created_at, fresh_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var holds []HeldReplacement
	for rows.Next() {
		var h HeldReplacement
		var since int64
		if err := rows.Scan(&h.FreshID, &h.OldID, &h.Owner, &since); err != nil {
			return nil, err
		}
		h.Since = decodeTime(since)
		holds = append(holds, h)
	}
	return holds, rows.Err()
}
