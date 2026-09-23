package store

import "database/sql"

// retiredRolePrefixes key settings rows that an older build wrote per
// session. Nothing reads them now; deleting a session still clears its own so
// a database carried forward from that build does not keep them forever.
var retiredRolePrefixes = []string{"asker/", "decides/", "answer/", "asked/"}

func unlinkRetiredRoles(tx *sql.Tx, id string) error {
	for _, prefix := range retiredRolePrefixes {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, prefix+id); err != nil {
			return err
		}
	}
	return nil
}
