package store

import (
	"database/sql"
	"fmt"
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
