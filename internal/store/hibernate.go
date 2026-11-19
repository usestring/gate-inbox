package store

import (
	"slices"
	"strings"

	"github.com/usestring/gate-inbox/internal/status"
)

// hibernatedSetting holds the ids hibernate stopped to give their memory
// back, and which come back on their own when a message arrives. A
// settings row like the parked set, for the same reason: a flag only a few
// rows carry does not earn a column and its migration.
//
// A hibernated row reads dead, so everything that treats a dead row as
// finished business has to ask this set first: the mail queue keeps what is
// sent to it instead of dropping it, and the child sweep leaves it on the
// active list instead of filing it away.
const hibernatedSetting = "hibernated_sessions"

// EndHibernated is a session hibernate stopped. Like park it is chosen, so
// crash restoration does not relaunch it; unlike park nothing brings the
// whole set back at once -- each row wakes when it is written to.
const EndHibernated = "hibernated"

// Hibernated returns the ids hibernate stopped and nothing has woken yet.
func (s *Store) Hibernated() ([]string, error) { return s.idSet(hibernatedSetting) }

// AddHibernated records ids as hibernated.
func (s *Store) AddHibernated(ids []string) error { return s.addToIDSet(hibernatedSetting, ids) }

// RemoveHibernated drops ids from the hibernated set.
func (s *Store) RemoveHibernated(ids []string) error {
	return s.removeFromIDSet(hibernatedSetting, ids)
}

// IsHibernated reports whether id is in the hibernated set.
func (s *Store) IsHibernated(id string) (bool, error) {
	ids, err := s.Hibernated()
	if err != nil {
		return false, err
	}
	return slices.Contains(ids, id), nil
}

// UndeliveredCount is how many messages are waiting for id: queued, not
// yet typed in, and not dropped.
func (s *Store) UndeliveredCount(id string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM session_inbox
		WHERE session_id = ? AND delivered_at = 0 AND dropped_at = 0`, id).Scan(&n)
	return n, err
}

// HibernatedToSettle is the hibernated ids that need something done, read
// in one query so the board can ask on every pass: a row with mail waiting
// is to be woken, and a row that is no longer dead (revived some other
// way), archived or gone is to leave the set.
func (s *Store) HibernatedToSettle() ([]string, error) {
	ids, err := s.Hibernated()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT s.id, s.status != 'dead' OR s.archived = 1 OR EXISTS (
		  SELECT 1 FROM session_inbox i
		  WHERE i.session_id = s.id AND i.delivered_at = 0 AND i.dropped_at = 0)
		FROM sessions s WHERE s.id IN (?`+strings.Repeat(", ?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]bool{}
	var settle []string
	for rows.Next() {
		var id string
		var due bool
		if err := rows.Scan(&id, &due); err != nil {
			return nil, err
		}
		found[id] = true
		if due {
			settle = append(settle, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if !found[id] {
			settle = append(settle, id)
		}
	}
	return settle, nil
}

// ApplyHibernated sets Status to hibernating on every live row in ids that
// the store holds dead, so the board and the tools say a hibernated session
// is resting rather than gone. A row that is no longer dead came back some
// other way and reads as what it is now; an archived row is filed away and
// stays dead. It rewrites sessions in place and returns it: call it on rows
// headed for display, never on the rows the next poll derives from, because
// everything that asks whether a pane is there still reads dead.
func ApplyHibernated(sessions []Session, ids []string) []Session {
	if len(ids) == 0 {
		return sessions
	}
	for i, sess := range sessions {
		sessions[i].Status = Shown(sess, ids)
	}
	return sessions
}

// Shown is sess's status as the board and the tools report it: hibernating
// for a dead row in the hibernated set, its stored status otherwise.
func Shown(sess Session, hibernated []string) string {
	if sess.Status == status.Dead && !sess.Archived && slices.Contains(hibernated, sess.ID) {
		return status.Hibernating
	}
	return sess.Status
}
