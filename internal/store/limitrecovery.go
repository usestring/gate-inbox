package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/internal/tracing"
)

const limitRecoveryPrefix = "limit-recovery:"

// LimitRecovery is a session's limit episode: the usage limit, or Codex
// capacity failure, its pane last showed, kept across manager restarts.
// internal/limitepisode records and reads it.
type LimitRecovery struct {
	Banner string
	// ResetAt is when the banner says the limit lifts, zero when it names no
	// time the board can read; for a capacity failure, the next retry.
	ResetAt  time.Time
	LaunchAt time.Time
	// AttemptedAt is when the board typed its one continuation in.
	AttemptedAt     time.Time
	CapacityDelay   time.Duration `json:",omitempty"`
	CapacityRetries int           `json:",omitempty"`
	// Account is the account the banner was raised on, "" for the CLI's own
	// login. A relaunch keeps it, so a session since moved to another
	// account reads as off the limit.
	Account string `json:",omitempty"`
	// SeenAt is when the board first saw the banner.
	SeenAt time.Time `json:",omitzero"`
	// Replayed says the banner on screen is one an earlier launch already
	// showed, which a relaunch drew again: no turn of this launch was cut.
	Replayed bool `json:",omitempty"`
	// Offered says the limit card was shown for the episode, whatever the
	// operator chose on it.
	Offered bool `json:",omitempty"`
}

func (s *Store) LimitRecoveries() (map[string]LimitRecovery, error) {
	if !tracing.Enabled() {
		return s.limitRecoveries()
	}
	start := time.Now()
	states, err := s.limitRecoveries()
	recordOp("store.LimitRecoveries", start, err, false, tracing.Attr{Key: "rows", Value: len(states)})
	return states, err
}

func (s *Store) limitRecoveries() (map[string]LimitRecovery, error) {
	rows, err := s.db.Query(`SELECT sessions.id, settings.value FROM settings
		JOIN sessions ON settings.key = ? || sessions.id WHERE sessions.archived = 0`, limitRecoveryPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := map[string]LimitRecovery{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var state LimitRecovery
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return nil, fmt.Errorf("read limit recovery for %s: %w", id, err)
		}
		states[id] = state
	}
	return states, rows.Err()
}

// The claim precedes the paste because a crash between them cannot prove
// whether Enter reached the agent. Competing managers must not retry it.
func (s *Store) CompareLimitRecovery(sess Session, previous, next *LimitRecovery) (bool, error) {
	key := limitRecoveryPrefix + sess.ID
	encode := func(state *LimitRecovery) (string, error) {
		if state == nil {
			return "", nil
		}
		data, err := json.Marshal(state)
		return string(data), err
	}
	oldValue, err := encode(previous)
	if err != nil {
		return false, err
	}
	if next == nil {
		result, err := s.db.Exec(`DELETE FROM settings WHERE key = ? AND value = ?`, key, oldValue)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		return n > 0, err
	}
	value, err := encode(next)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`INSERT INTO settings (key, value)
		SELECT ?, ? WHERE EXISTS (SELECT 1 FROM sessions WHERE id = ? AND archived = 0
		AND CASE WHEN agent_launched_at = 0 THEN created_at ELSE agent_launched_at END IN (?, ?))
		AND (? = '' OR EXISTS (SELECT 1 FROM settings WHERE key = ? AND value = ?))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value WHERE settings.value = ?`,
		key, value, sess.ID, encodeTime(sess.LaunchTime()), sess.LaunchTime().Unix(), oldValue, key, oldValue, oldValue)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}
