package store

import (
	"database/sql"
	"errors"
	"time"
)

const (
	ReservationExclusive = "exclusive"
	ReservationShared    = "shared"
)

// Reservation is an advisory lease over a set of paths. It is not a lock:
// nothing stops an agent editing a reserved file. Its job is to surface
// the collision while both agents can still talk about it, which is what
// a worktree cannot do, since worktrees defer the conflict to merge time.
type Reservation struct {
	ID         string
	SessionID  string
	Pattern    string
	Mode       string
	Note       string
	AcquiredAt time.Time
	ExpiresAt  time.Time
}

// Reserve takes or extends this session's leases on a set of patterns and
// returns the live leases held by others that overlap them. Reading the
// overlaps and taking every lease is one transaction: two agents reserving
// the same path at once would otherwise both read the list before either
// wrote and neither would hear about the other, and a failure on a later
// pattern would leave earlier ones granted without their caller knowing it
// holds them. A session renewing its own lease is not a conflict with itself.
func (s *Store) Reserve(reservations []Reservation) ([]Reservation, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := resolveLegacyReservationPaths(tx); err != nil {
		return nil, err
	}

	var conflicts []Reservation
	// Report a lease once even if it overlaps several requested patterns.
	seen := map[string]bool{}
	directories := map[string]string{}
	for _, reservation := range reservations {
		dir, ok := directories[reservation.SessionID]
		if !ok {
			dir, err = reservationDirectory(tx, reservation.SessionID)
			if err != nil {
				return nil, err
			}
			directories[reservation.SessionID] = dir
		}
		path := resolveReservationPath(dir, reservation.Pattern)
		found, err := overlappingReservations(tx, reservation, path)
		if err != nil {
			return nil, err
		}
		for _, conflict := range found {
			if seen[conflict.ID] {
				continue
			}
			seen[conflict.ID] = true
			conflicts = append(conflicts, conflict)
		}
		if err := writeReservation(tx, reservation, path); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return conflicts, nil
}

func writeReservation(tx *sql.Tx, reservation Reservation, path reservationPath) error {
	if path.resolved != "" {
		// The old store could hold both spellings as separate leases. Renew
		// the oldest lease in place and keep its original spelling.
		var id string
		err := tx.QueryRow(`
UPDATE file_reservations SET mode = ?, note = ?, expires_at = ?
 WHERE id = (
   SELECT id FROM file_reservations WHERE session_id = ? AND resolved_path = ?
   ORDER BY acquired_at, id LIMIT 1
 )
 RETURNING id`,
			reservation.Mode, reservation.Note, encodeTime(reservation.ExpiresAt), reservation.SessionID, path.resolved).Scan(&id)
		if err == nil {
			_, err = tx.Exec(`
DELETE FROM file_reservations WHERE session_id = ? AND resolved_path = ? AND id != ?`,
				reservation.SessionID, path.resolved, id)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	_, err := tx.Exec(`
INSERT INTO file_reservations
	(id, session_id, pattern, mode, note, acquired_at, expires_at, resolved_path, match_glob)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, pattern) DO UPDATE SET
	mode = excluded.mode, note = excluded.note, expires_at = excluded.expires_at,
	resolved_path = excluded.resolved_path, match_glob = excluded.match_glob`,
		reservation.ID, reservation.SessionID, reservation.Pattern, reservation.Mode,
		reservation.Note, encodeTime(reservation.AcquiredAt), encodeTime(reservation.ExpiresAt),
		path.resolved, path.glob)
	return err
}

// overlappingReservations finds live leases over the same paths held by
// someone else. It takes the transaction because the store holds a single
// connection: a query issued on s.db while a Tx holds it waits forever.
//
// GLOB checks a pattern against a path in either order. It does not compute
// intersections between two globs.
func overlappingReservations(tx *sql.Tx, reservation Reservation, path reservationPath) ([]Reservation, error) {
	scoped := path.resolved != ""
	if !scoped {
		// A relative pattern without a known directory cannot match a resolved path.
		path = reservationPath{resolved: reservation.Pattern, glob: reservation.Pattern}
	}
	rows, err := tx.Query(`
SELECT id, session_id, pattern, mode, note, acquired_at, expires_at
  FROM file_reservations
 WHERE session_id != ? AND expires_at > ?
   AND (resolved_path <> '') = ?
   AND (COALESCE(NULLIF(resolved_path, ''), pattern) = ?
     OR COALESCE(NULLIF(resolved_path, ''), pattern) GLOB ?
     OR ? GLOB COALESCE(NULLIF(match_glob, ''), pattern))
   AND (mode = ? OR ? = ?)
 ORDER BY acquired_at`,
		reservation.SessionID, encodeTime(reservation.AcquiredAt), scoped,
		path.resolved, path.glob, path.resolved,
		ReservationExclusive, reservation.Mode, ReservationExclusive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReservations(rows)
}

// Release drops one pattern's lease, or every lease this session holds
// when the pattern is empty.
func (s *Store) Release(sessionID, pattern string) (int64, error) {
	if pattern == "" {
		res, err := s.db.Exec(`DELETE FROM file_reservations WHERE session_id = ?`, sessionID)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := resolveLegacyReservationPaths(tx); err != nil {
		return 0, err
	}
	resolved, err := reservationPathForRelease(tx, sessionID, pattern)
	if err != nil {
		return 0, err
	}
	res, err := tx.Exec(`
DELETE FROM file_reservations WHERE session_id = ?
  AND ((resolved_path <> '' AND resolved_path = ?) OR (resolved_path = '' AND pattern = ?))`,
		sessionID, resolved, pattern)
	if err != nil {
		return 0, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func reservationPathForRelease(tx *sql.Tx, sessionID, pattern string) (string, error) {
	// A listed spelling still names its original lease after the session
	// moves, even if that spelling now resolves to a different lease.
	var resolved string
	err := tx.QueryRow(`SELECT resolved_path FROM file_reservations WHERE session_id = ? AND pattern = ?`,
		sessionID, pattern).Scan(&resolved)
	if !errors.Is(err, sql.ErrNoRows) {
		return resolved, err
	}
	dir, err := reservationDirectory(tx, sessionID)
	if err != nil {
		return "", err
	}
	return resolveReservationPath(dir, pattern).resolved, nil
}

// Reservations lists every lease still live at now. An expired lease is
// left in place for the next sweep rather than hidden behind a delete,
// so a crashed agent's claim lapses instead of holding the repo.
func (s *Store) Reservations(now time.Time) ([]Reservation, error) {
	rows, err := s.db.Query(`
SELECT id, session_id, pattern, mode, note, acquired_at, expires_at
  FROM file_reservations WHERE expires_at > ? ORDER BY acquired_at, pattern`, encodeTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReservations(rows)
}

func (s *Store) PruneReservations(now time.Time) error {
	_, err := s.db.Exec(`DELETE FROM file_reservations WHERE expires_at <= ?`, encodeTime(now))
	return err
}

func scanReservations(rows rowScanner) ([]Reservation, error) {
	reservations := make([]Reservation, 0)
	for rows.Next() {
		var reservation Reservation
		var acquiredAt, expiresAt int64
		if err := rows.Scan(&reservation.ID, &reservation.SessionID, &reservation.Pattern,
			&reservation.Mode, &reservation.Note, &acquiredAt, &expiresAt); err != nil {
			return nil, err
		}
		reservation.AcquiredAt = decodeTime(acquiredAt)
		reservation.ExpiresAt = decodeTime(expiresAt)
		reservations = append(reservations, reservation)
	}
	return reservations, rows.Err()
}

type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}
