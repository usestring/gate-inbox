package store

import (
	"database/sql"
	"time"
)

// Session work is what a session made that no forge or tracker answers for:
// a page it published, today. Pull requests and tickets are found by reading
// what the session said and then asking GitHub or Linear; a published page
// has nobody to ask, so whoever saw it published -- an extension's tool, or
// the board reading Claude Code's own Artifact tool -- writes it here, and
// the board reads it back on every refresh. Like session_prs, it is stored
// per session so a restart onto a new conversation keeps it.
//
// An item is keyed by who attached it (source), its kind and an id the
// source chose, so attaching the same id again -- a republish -- replaces
// the address and title rather than adding a second row.

// SessionWork is one item attached to a session.
type SessionWork struct {
	// Source is who attached it: an extension's id, or "" for the board.
	Source string
	Kind   string
	// ID is the source's own name for the item; attaching it again under the
	// same ID updates it.
	ID    string
	URL   string
	Title string
	// FirstSeen is when it was first attached; UpdatedAt is the latest.
	FirstSeen time.Time
	UpdatedAt time.Time
}

// AttachSessionWork records items against a session, replacing the address
// and title of any it already holds under the same source, kind and ID. An
// item the store already holds exactly costs no write.
func (s *Store) AttachSessionWork(sessionID string, items []SessionWork, now time.Time) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO session_work (session_id, source, kind, item_id, url, title, first_seen, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (session_id, source, kind, item_id) DO UPDATE SET
			url = excluded.url, title = excluded.title, updated_at = excluded.updated_at
		WHERE url != excluded.url OR title != excluded.title`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	at := now.UnixMilli()
	for _, item := range items {
		if item.Kind == "" || item.ID == "" || item.URL == "" {
			continue
		}
		if _, err := stmt.Exec(sessionID, item.Source, item.Kind, item.ID, item.URL, item.Title, at, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SessionWorkItems is every item attached to a session, oldest first.
func (s *Store) SessionWorkItems(sessionID string) ([]SessionWork, error) {
	rows, err := s.db.Query(`SELECT source, kind, item_id, url, title, first_seen, updated_at FROM session_work
		WHERE session_id = ? ORDER BY first_seen, source, kind, item_id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SessionWork
	for rows.Next() {
		var item SessionWork
		var first, updated int64
		if err := rows.Scan(&item.Source, &item.Kind, &item.ID, &item.URL, &item.Title, &first, &updated); err != nil {
			return nil, err
		}
		item.FirstSeen, item.UpdatedAt = time.UnixMilli(first), time.UnixMilli(updated)
		items = append(items, item)
	}
	return items, rows.Err()
}

func deleteSessionWork(tx *sql.Tx, sessionID string) error {
	_, err := tx.Exec(`DELETE FROM session_work WHERE session_id = ?`, sessionID)
	return err
}

// copySessionWork gives a migrated session's new row the work its source
// had; the source keeps its record, as with its pull requests.
func copySessionWork(tx *sql.Tx, fromID, toID string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO session_work (session_id, source, kind, item_id, url, title, first_seen, updated_at)
		SELECT ?, source, kind, item_id, url, title, first_seen, updated_at FROM session_work WHERE session_id = ?`, toID, fromID)
	return err
}
