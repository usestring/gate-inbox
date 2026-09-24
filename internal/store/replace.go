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
	var group, parentID string
	var order int64
	err = tx.QueryRow(`SELECT group_name, parent_id, sort_order FROM sessions WHERE id = ?`, oldID).Scan(&group, &parentID, &order)
	if errors.Is(err, sql.ErrNoRows) {
		return Replacement{}, fmt.Errorf("session %s: %w", oldID, err)
	}
	if err != nil {
		return Replacement{}, err
	}
	sess.Group = group
	sess.ParentID = parentID
	if sess.SpawnedBy == "" {
		sess.SpawnedBy = parentID
	}
	now := time.Now()
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now
	}
	if sess.LastStatusAt.IsZero() {
		sess.LastStatusAt = sess.CreatedAt
	}
	pendingInputs, err := encodePendingInputs(sess.PendingInputs)
	if err != nil {
		return Replacement{}, err
	}
	// Directly after the old row, so the replacement is drawn where the
	// operator was already looking.
	if _, err := tx.Exec(`UPDATE sessions SET sort_order = sort_order + 1
		 WHERE group_name = ? AND parent_id = ? AND sort_order > ?`, group, parentID, order); err != nil {
		return Replacement{}, err
	}
	if _, err := tx.Exec(
		`INSERT INTO sessions (id, name, tool, cwd, group_name, status, archived, created_at, last_status_at, agent_session_id, tmux_socket, tmux_pane_id, pending_inputs, parent_id, spawned_by, launch_prompt, model, account, name_source, priority_tier, role, sort_order)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.Name, sess.Tool, sess.Cwd, sess.Group, sess.Status,
		encodeTime(sess.CreatedAt), encodeTime(sess.LastStatusAt), sess.AgentSessionID,
		sess.TmuxSocket, sess.TmuxPaneID, pendingInputs, sess.ParentID, sess.SpawnedBy, sess.LaunchPrompt, sess.Model, sess.Account,
		nameSourceOr(sess.NameSource), string(sess.Priority), sess.Role, order+1,
	); err != nil {
		return Replacement{}, err
	}
	var moved Replacement
	res, err := tx.Exec(`
UPDATE session_inbox SET session_id = ?, claimed_at = 0
 WHERE session_id = ? AND delivered_at = 0 AND dropped_at = 0`, sess.ID, oldID)
	if err != nil {
		return Replacement{}, err
	}
	if moved.Forwarded, err = res.RowsAffected(); err != nil {
		return Replacement{}, err
	}
	res, err = tx.Exec(`UPDATE file_reservations SET session_id = ? WHERE session_id = ?`, sess.ID, oldID)
	if err != nil {
		return Replacement{}, err
	}
	if moved.Reservations, err = res.RowsAffected(); err != nil {
		return Replacement{}, err
	}
	retire := `UPDATE sessions SET status = 'dead', last_status_at = ? WHERE id = ?`
	args := []any{encodeTime(now), oldID}
	if snapshot != "" {
		retire = `UPDATE sessions SET status = 'dead', last_status_at = ?, snapshot = ? WHERE id = ?`
		args = []any{encodeTime(now), snapshot, oldID}
	}
	if _, err := tx.Exec(retire, args...); err != nil {
		return Replacement{}, err
	}
	if launch != nil {
		if err := launch(); err != nil {
			return Replacement{}, err
		}
	}
	return moved, tx.Commit()
}
