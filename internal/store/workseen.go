package store

import (
	"strings"
	"time"
)

// A settled artifact -- a merged or closed pull request, a completed or
// cancelled ticket -- leaves the board a while after the operator first had
// it on screen in that state. The clock starts at that first sighting, so it
// has to outlive the process: a restart that forgot every sighting would put
// the whole settled backlog back for another day.
//
// Only the sighting is stored. Whether the artifact is still settled is the
// source's to say and is fetched live; a mark on something that reopened is
// dropped on the fetch that notices.

// WorkSeen is every recorded sighting, keyed the way the work tracker keys
// its references.
func (s *Store) WorkSeen() (map[string]time.Time, error) {
	rows, err := s.db.Query(`SELECT key, seen_at FROM work_seen`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]time.Time{}
	for rows.Next() {
		var key string
		var at int64
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		seen[key] = time.UnixMilli(at)
	}
	return seen, rows.Err()
}

// RecordWorkSeen writes sightings. A key already recorded keeps its earlier
// time: the clock runs from the first sighting, and a later frame drawing the
// same row is not a new one.
func (s *Store) RecordWorkSeen(seen map[string]time.Time) error {
	if len(seen) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO work_seen (key, seen_at) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for key, at := range seen {
		if _, err := stmt.Exec(key, at.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ForgetWorkSeen drops the sightings for these keys.
func (s *Store) ForgetWorkSeen(keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	args := make([]any, len(keys))
	for i, key := range keys {
		args[i] = key
	}
	_, err := s.db.Exec(`DELETE FROM work_seen WHERE key IN (?`+strings.Repeat(",?", len(keys)-1)+`)`, args...)
	return err
}
