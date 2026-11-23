package store

import (
	"database/sql"
	"errors"
)

const DefaultFanoutLimit = 6

var ErrFanoutLimit = errors.New("the default agent fan-out limit is reached")

type spawnBudgetPolicy int

const (
	unmeteredSpawn spawnBudgetPolicy = iota
	defaultSpawnBudget
	approvedExtraSpawn
)

const (
	spawnFleetPrefix = "spawn_fleet/"
	spawnCountPrefix = "spawn_count/"
)

// LaunchAgentSession charges the caller's fleet in the same transaction as
// the launch. Concurrent CLIs cannot both spend the last unapproved slot.
func (s *Store) LaunchAgentSession(sess Session, anchorID string, approved bool, launch func() error) error {
	policy := defaultSpawnBudget
	if approved {
		policy = approvedExtraSpawn
	}
	return s.createSession(sess, anchorID, false, policy, launch)
}

func (s *Store) FanoutLimitReached(callerID string) (bool, error) {
	_, count, err := spawnBudget(s.db, callerID)
	return count >= DefaultFanoutLimit, err
}

func spawnBudget(db execer, callerID string) (fleet string, count int, err error) {
	// Stored fleet IDs survive placement changes and pruning of ancestors.
	// Walk existing spawner links for rows made before budget tracking.
	err = db.QueryRow(`
		WITH RECURSIVE lineage(id, spawner, fleet) AS (
			SELECT s.id, `+spawnerColumnOf("s")+`, COALESCE(f.value, '')
			FROM sessions s LEFT JOIN settings f ON f.key = ? || s.id
			WHERE s.id = ?
			UNION
			SELECT s.id, `+spawnerColumnOf("s")+`, COALESCE(f.value, '')
			FROM sessions s JOIN lineage l ON s.id = l.spawner AND l.fleet = ''
			LEFT JOIN settings f ON f.key = ? || s.id
		), root(id) AS (
			SELECT COALESCE(NULLIF(l.fleet, ''), l.id) FROM lineage l
			WHERE l.fleet != '' OR NOT EXISTS (SELECT 1 FROM sessions WHERE id = l.spawner)
		)
		SELECT r.id, COALESCE(c.value, '0')
		FROM root r LEFT JOIN settings c ON c.key = ? || r.id`,
		spawnFleetPrefix, callerID, spawnFleetPrefix, spawnCountPrefix,
	).Scan(&fleet, &count)
	return fleet, count, err
}

func chargeSpawnBudget(tx *sql.Tx, sess Session, policy spawnBudgetPolicy) error {
	fleet, count, err := spawnBudget(tx, sess.SpawnedBy)
	if err != nil {
		return err
	}
	if count >= DefaultFanoutLimit && policy != approvedExtraSpawn {
		return ErrFanoutLimit
	}
	if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?), (?, ?)
		ON CONFLICT(key) DO NOTHING`, spawnFleetPrefix+sess.SpawnedBy, fleet, spawnFleetPrefix+sess.ID, fleet); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, spawnCountPrefix+fleet, count+1)
	return err
}

func inheritSpawnBudget(tx *sql.Tx, fromID, toID string) error {
	_, err := tx.Exec(`INSERT INTO settings (key, value)
		SELECT ?, value FROM settings WHERE key = ?`, spawnFleetPrefix+toID, spawnFleetPrefix+fromID)
	return err
}

func unlinkSpawnBudget(tx *sql.Tx, id string) error {
	var fleet string
	err := tx.QueryRow(`DELETE FROM settings WHERE key = ? RETURNING value`, spawnFleetPrefix+id).Scan(&fleet)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM settings WHERE key = ?
		AND NOT EXISTS (SELECT 1 FROM settings WHERE key GLOB ? AND value = ?)`,
		spawnCountPrefix+fleet, spawnFleetPrefix+"*", fleet)
	return err
}
