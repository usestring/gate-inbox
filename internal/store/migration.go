package store

import (
	"database/sql"
	"fmt"
	"time"
)

const migrationOpeningPrefix = "migration_opening/"
const migrationOpeningColumn = `COALESCE((SELECT value FROM settings WHERE key = 'migration_opening/' || sessions.id), '[]')`

const migrationKeyPrefix = "migration_link/"
const migrationColumn = `COALESCE((SELECT value FROM settings WHERE key = 'migration_link/' || sessions.id), '')`

func Linked(a, b Session) bool {
	return a.MigrationID != "" && a.MigrationID == b.MigrationID && a.Group == b.Group && a.ParentID == b.ParentID
}

func migrationBlocks(sessions []Session) [][]Session {
	type key struct{ link, group, parent, single string }
	indices := make(map[key]int)
	var blocks [][]Session
	for _, sess := range sessions {
		k := key{link: sess.MigrationID, group: sess.Group, parent: sess.ParentID}
		if k.link == "" {
			k.single = sess.ID
		}
		i, ok := indices[k]
		if !ok {
			i = len(blocks)
			indices[k] = i
			blocks = append(blocks, nil)
		}
		blocks[i] = append(blocks[i], sess)
	}
	return blocks
}

func flattenMigrationBlocks(blocks [][]Session) []Session {
	var sessions []Session
	for _, block := range blocks {
		sessions = append(sessions, block...)
	}
	return sessions
}

func OrderLinkedSessions(sessions []Session) []Session {
	return flattenMigrationBlocks(migrationBlocks(sessions))
}

func SwapLinkedSessions(sessions []Session, id, targetID string) ([]Session, error) {
	blocks := migrationBlocks(sessions)
	current, target := -1, -1
	for i, block := range blocks {
		for _, sess := range block {
			if sess.ID == id {
				current = i
			}
			if sess.ID == targetID {
				target = i
			}
		}
	}
	if current < 0 || target < 0 {
		return nil, fmt.Errorf("session order changed while reordering")
	}
	blocks[current], blocks[target] = blocks[target], blocks[current]
	return flattenMigrationBlocks(blocks), nil
}

func unlinkMigration(tx *sql.Tx, id string) error {
	if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, migrationOpeningPrefix+id); err != nil {
		return err
	}
	var link string
	if err := tx.QueryRow(`SELECT COALESCE((SELECT value FROM settings WHERE key = ?), '')`, migrationKeyPrefix+id).Scan(&link); err != nil {
		return err
	}
	if link == "" {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, migrationKeyPrefix+id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM settings WHERE key LIKE 'migration_link/%' AND value = ?
	 AND (SELECT COUNT(*) FROM settings WHERE key LIKE 'migration_link/%' AND value = ?) <= 1`, link, link)
	return err
}

// CompleteMigration files fromID away now that toID, the row a migration
// made of it, is up: toID takes over the sessions fromID spawned and drew
// under it and the reports they queued for it that nothing has started
// delivering, gets a copy of the pull requests fromID opened, and fromID is
// archived. Until this runs the source is left
// exactly as it was, so a migration that fails on the way loses nothing.
//
// One transaction, so no reader sees the children still filed under a
// retired source, which the child sweep reads as a spawner gone and files
// them away. Ending the source's pane is the caller's, after this commits.
func (s *Store) CompleteMigration(fromID, toID string) error {
	if fromID == "" || toID == "" || fromID == toID {
		return fmt.Errorf("a migration needs a new id distinct from %q", fromID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := adoptMigratedChildren(tx, fromID, toID); err != nil {
		return err
	}
	// The board finds a session's pull requests by its id, and they are the
	// new row's work as much as the old one's; the source keeps its record.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO session_prs (session_id, url, first_seen)
		 SELECT ?, url, first_seen FROM session_prs WHERE session_id = ?`, toID, fromID); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE sessions SET archived = 1, archived_at = ? WHERE id = ?`, encodeTime(time.Now()), fromID)
	if err != nil {
		return err
	}
	if err := requireRow(res, fromID); err != nil {
		return err
	}
	var parentID string
	if err := tx.QueryRow(`SELECT parent_id FROM sessions WHERE id = ?`, fromID).Scan(&parentID); err != nil {
		return err
	}
	if parentID != "" {
		if _, err := tx.Exec(`INSERT INTO child_archives (parent_id, done) VALUES (?, 1)
		 ON CONFLICT(parent_id) DO UPDATE SET done = done + 1`, parentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// adoptMigratedChildren hands what fromID tracks to toID, the row a
// migration just made of it: the sessions it spawned and drew under it, and
// the reports they queued for it that nothing has started delivering. A
// detached spawn stays a record of who made it. A message already claimed is
// being typed into fromID and is left to finish there.
func adoptMigratedChildren(tx *sql.Tx, fromID, toID string) error {
	if _, err := tx.Exec(`UPDATE sessions SET spawned_by = ? WHERE spawned_by = ? AND parent_id != '' AND id != ?`, toID, fromID, toID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET parent_id = ? WHERE parent_id = ? AND id != ?`, toID, fromID, toID); err != nil {
		return err
	}
	_, err := tx.Exec(`
UPDATE session_inbox SET session_id = ?
 WHERE session_id = ? AND delivered_at = 0 AND dropped_at = 0 AND claimed_at = 0
   AND sender_id IN (SELECT s.id FROM sessions s WHERE s.parent_id != '' AND `+spawnerColumnOf("s")+` = ?)`,
		toID, fromID, toID)
	return err
}
