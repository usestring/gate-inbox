package store

import (
	"database/sql"
	"time"
)

// Opened pull requests are the one fact about a session's work worth keeping
// outside the transcript. The history index finds them by reading the
// transcript, and the transcript is not permanent: a restart moves the
// session onto a new conversation and the old file stops being indexed, and
// the CLIs delete their own history on a retention clock. A pull request the
// session opened is still its work after both, so the fact is written once
// here and read back alongside whatever the index currently holds.
//
// Only the identity is stored here, and it is stored per session: this table
// answers "which pull requests are this session's work", which is a fact about
// the session and stays true whatever GitHub later says. What GitHub says
// lives in forge_prs, keyed by the reference rather than the session, and is
// restored as an explicitly unverified value rather than a current one --
// forgestate.go carries why that distinction is what makes storing it safe.

// RecordOpenedPRs remembers that a session opened the pull requests at these
// URLs. Rows the store already has are left alone, so calling it with the
// whole set the index knows is idempotent and costs one transaction.
func (s *Store) RecordOpenedPRs(sessionID string, urls []string, now time.Time) error {
	if len(urls) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO session_prs (session_id, url, first_seen) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	at := now.UnixMilli()
	for _, url := range urls {
		if url == "" {
			continue
		}
		if _, err := stmt.Exec(sessionID, url, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// OpenedPRs is every pull request URL recorded for a session, oldest first.
func (s *Store) OpenedPRs(sessionID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT url FROM session_prs WHERE session_id = ? ORDER BY first_seen, url`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var urls []string
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, err
		}
		urls = append(urls, url)
	}
	return urls, rows.Err()
}

func deleteOpenedPRs(tx *sql.Tx, sessionID string) error {
	_, err := tx.Exec(`DELETE FROM session_prs WHERE session_id = ?`, sessionID)
	return err
}
