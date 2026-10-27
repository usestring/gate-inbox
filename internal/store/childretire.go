package store

import (
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

// Retiring a child whose finish its spawner has already taken in.
//
// The exited-child sweep in AutoArchivableChildren files what has already
// stopped. A child that finished its turn still has an agent in its pane, and
// ending one because it came to rest is how eight working children of one
// parent were killed on 2026-09-11. What makes a finished child safe to end
// is not the rest itself but the spawner having taken it in and then left it
// alone: the rest notice was typed into the spawner's prompt, or the spawner
// read or waited on the child after it finished, and nothing has moved since.
//
// Absorption is measured against the child's current rest. A notice or a read
// from an earlier turn says nothing about this one, so both have to be no
// older than last_status_at, which every status change moves forward. A
// spawner that sends a follow-up puts the child back to work, and that moves
// it too.

// RetirableChild is a finished child whose spawner has taken in the finish,
// with when it did.
type RetirableChild struct {
	ID         string
	AbsorbedAt time.Time
	// Via says which fact counted: the rest notice delivered, or the spawner
	// reading or waiting on the child.
	Via string
}

const (
	// AbsorbedByNotice is a finish whose rest notice reached the spawner.
	AbsorbedByNotice = "rest notice delivered to its spawner"
	// AbsorbedByRead is a finish the spawner read or waited on.
	AbsorbedByRead = "spawner read or waited on it after it finished"
	// AbsorbedBySpawnerGone is a finish with nobody left to take it in: the
	// spawner's row is deleted, archived or dead. On 2026-10-12 one such
	// child had sat finished for 217 hours with its agent still running.
	// A dead spawner counts from when it died, not from the finish: a
	// restart in place (an account switch, a grant) reads dead between
	// ending the pane and relaunching it, and counting from the finish
	// archived every finished child of a parent that was only restarting.
	AbsorbedBySpawnerGone = "its spawner is gone"
)

// RetirableChildren is every finished or idle child whose spawner took in the finish
// at or before cutoff, which is the grace window already applied.
//
// Left out, whatever else holds: a child its spawner asked to keep, a pane the
// manager adopted rather than started, and a child with a message queued for
// it that it has not read yet -- the follow-up the grace window is there for.
// Whether it has live children of its own is the caller's to check, since that
// needs the whole tree and the caller already holds it.
func (s *Store) RetirableChildren(cutoff time.Time) ([]RetirableChild, error) {
	rows, err := s.db.Query(
		`SELECT s.id,
		        COALESCE((
		          SELECT MIN(i.delivered_at) FROM session_inbox i
		          WHERE i.session_id = `+spawnerColumnOf("s")+` AND i.sender_id = s.id
		            AND i.subject = ? || s.id
		            AND i.delivered_at != 0
		            AND i.sent_at >= s.last_status_at
		        ), 0) AS noticed,
		        CASE WHEN s.spawner_read_at >= s.last_status_at THEN s.spawner_read_at ELSE 0 END AS seen,
		        CASE WHEN NOT EXISTS (
		          SELECT 1 FROM sessions p
		          WHERE p.id = `+spawnerColumnOf("s")+` AND p.archived = 0
		        ) THEN s.last_status_at ELSE COALESCE((
		          SELECT MAX(s.last_status_at, p.last_status_at) FROM sessions p
		          WHERE p.id = `+spawnerColumnOf("s")+` AND p.status = ?
		        ), 0) END AS orphaned
		 FROM sessions s
		 WHERE s.parent_id != '' AND s.archived = 0
		   AND s.status IN (?, ?)
		   AND s.keep_child = 0
		   AND s.tmux_pane_id = ''
		   AND s.last_status_at != 0
		   AND NOT EXISTS (
		     SELECT 1 FROM session_inbox q
		     WHERE q.session_id = s.id AND q.delivered_at = 0
		       AND q.dropped_at = 0 AND q.superseded_by = 0
		   )
		 ORDER BY s.id`, ChildRestSubject(""), status.Dead, status.Finished, status.Idle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	limit := encodeTime(cutoff)
	var children []RetirableChild
	for rows.Next() {
		var id string
		var noticed, read, orphaned int64
		if err := rows.Scan(&id, &noticed, &read, &orphaned); err != nil {
			return nil, err
		}
		at, via := noticed, AbsorbedByNotice
		if read != 0 && (at == 0 || read < at) {
			at, via = read, AbsorbedByRead
		}
		if orphaned != 0 && (at == 0 || orphaned < at) {
			at, via = orphaned, AbsorbedBySpawnerGone
		}
		if at == 0 || at > limit {
			continue
		}
		children = append(children, RetirableChild{ID: id, AbsorbedAt: decodeTime(at), Via: via})
	}
	return children, rows.Err()
}

// SetKeepChild marks a child as one its spawner wants kept, so no automatic
// cleanup files it away, or clears the mark.
func (s *Store) SetKeepChild(id string, keep bool) error {
	res, err := s.db.Exec(`UPDATE sessions SET keep_child = ? WHERE id = ?`, boolToInt(keep), id)
	if err != nil {
		return err
	}
	return requireRow(res, id)
}

// KeepChild reports whether the spawner asked to keep id.
func (s *Store) KeepChild(id string) (bool, error) {
	var keep int
	if err := s.db.QueryRow(`SELECT keep_child FROM sessions WHERE id = ?`, id).Scan(&keep); err != nil {
		return false, err
	}
	return keep != 0, nil
}

// NoteSpawnerRead records that id's spawner read or waited on it at at. Only
// a finished child is stamped: a read of a child mid-turn is progress, not the
// finish being taken in.
func (s *Store) NoteSpawnerRead(id string, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET spawner_read_at = ? WHERE id = ? AND status = ?`,
		encodeTime(at), id, status.Finished)
	return err
}

// Descendants is every row under id by ownership rather than by drawing:
// what id spawned, what those spawned, and so on, nearest first. parent_id is
// one level deep on purpose, so a grandchild is filed under the root and only
// spawned_by says whose it is; see spawner.go. A detached spawn is nobody's
// descendant, so archiving its creator leaves it running.
func Descendants(sessions []Session, id string) []Session {
	byOwner := make(map[string][]Session, len(sessions))
	for _, sess := range sessions {
		if owner := TrackerOf(sess); owner != "" {
			byOwner[owner] = append(byOwner[owner], sess)
		}
	}
	var out []Session
	visited := map[string]bool{id: true}
	frontier := []string{id}
	for len(frontier) > 0 {
		var next []string
		for _, owner := range frontier {
			for _, kid := range byOwner[owner] {
				if visited[kid.ID] {
					continue
				}
				visited[kid.ID] = true
				out = append(out, kid)
				next = append(next, kid.ID)
			}
		}
		frontier = next
	}
	return out
}
