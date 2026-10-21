package store

import "time"

// ChildRow is the little of a child's row a parent needs to tell whether it
// is stuck on a dialog: who it is, what runs it, where its transcript is and
// the status the board last gave it.
type ChildRow struct {
	ID             string
	Name           string
	Tool           string
	Cwd            string
	Status         string
	AgentSessionID string
	LastStatusAt   time.Time
}

// OpenExisting is a handle on a store some process has already brought up to
// date, without the migrations Open runs. Those scan every session row on a
// cold connection, which a hook or a one-shot command pays on every call; a
// caller whose query fails on an older file falls back to Open.
func OpenExisting(path string) (*Store, error) {
	return connect(path)
}

// LiveChildren is every unarchived row trackerID follows (TrackerOf), in id
// order: the rows whose dialogs it answers. It reads only the columns
// ChildRow carries rather than decoding every row on the board.
func (s *Store) LiveChildren(trackerID string) ([]ChildRow, error) {
	if trackerID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT id, name, tool, cwd, status, agent_session_id, last_status_at
		 FROM sessions
		 WHERE archived = 0 AND parent_id != '' AND `+spawnerColumnOf("sessions")+` = ?
		 ORDER BY id`, trackerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var kids []ChildRow
	for rows.Next() {
		var row ChildRow
		var lastStatus int64
		if err := rows.Scan(&row.ID, &row.Name, &row.Tool, &row.Cwd, &row.Status, &row.AgentSessionID, &lastStatus); err != nil {
			return nil, err
		}
		row.LastStatusAt = decodeTime(lastStatus)
		kids = append(kids, row)
	}
	return kids, rows.Err()
}
