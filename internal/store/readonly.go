package store

import (
	"database/sql"
	"net/url"
	"path/filepath"
)

// OpenReadOnly opens an existing database for reading only. It runs no
// migrations and sets no journal mode, so a report over a live board's
// state.db cannot change it: SQLite refuses every write on the handle. A
// database older than this build's schema reads with whatever columns its
// queries can find, and the caller sees the error of one that is missing.
func OpenReadOnly(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

// WaitingMessages are the inbox messages still due a delivery: not typed
// in, not dropped, and not replaced by a later one on the same subject.
func (s *Store) WaitingMessages() ([]InboxMessage, error) {
	rows, err := s.db.Query(`
SELECT id, session_id, sender_id, sender_name, subject, sent_at, claimed_at
  FROM session_inbox
 WHERE delivered_at = 0 AND dropped_at = 0 AND superseded_by = 0
 ORDER BY sent_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []InboxMessage
	for rows.Next() {
		var msg InboxMessage
		var sentAt, claimedAt int64
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.SenderID, &msg.SenderName, &msg.Subject, &sentAt, &claimedAt); err != nil {
			return nil, err
		}
		msg.SentAt = decodeTime(sentAt)
		msg.ClaimedAt = decodeTime(claimedAt)
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}
