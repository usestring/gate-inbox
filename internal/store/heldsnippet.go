package store

import (
	"fmt"
	"strconv"
	"strings"
)

const SnippetSenderID = HumanSenderID + "/snippet"

const heldSnippetPrefix = "held-snippet:"

func (s *Store) EnqueueHeldSnippet(msg InboxMessage, handOver bool) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := s.insertMessage(tx, msg, DefaultInboxLimits)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, heldSnippetPrefix+strconv.FormatInt(id, 10), strconv.FormatBool(handOver)); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) HeldSnippets() (map[int64]bool, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings WHERE key LIKE 'held-snippet:%'
 UNION ALL
 SELECT 'held-snippet:' || i.id, 'false' FROM session_inbox i
 WHERE i.sender_id = ? AND i.delivered_at = 0 AND i.dropped_at = 0 AND i.superseded_by = 0
 AND NOT EXISTS (SELECT 1 FROM settings WHERE key = 'held-snippet:' || i.id)`, SnippetSenderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	held := map[int64]bool{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(key, heldSnippetPrefix), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("held snippet key: %w", err)
		}
		handOver, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("held snippet intent: %w", err)
		}
		held[id] = handOver
	}
	return held, rows.Err()
}

func (s *Store) ForgetHeldSnippet(id int64) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, heldSnippetPrefix+strconv.FormatInt(id, 10))
	return err
}

func (s *Store) KeepHeldSnippetsHere() error {
	_, err := s.db.Exec(`UPDATE settings SET value = 'false' WHERE key LIKE 'held-snippet:%'`)
	return err
}
