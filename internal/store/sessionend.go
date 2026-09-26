package store

import (
	"time"
)

// A dead row says the agent is gone, never why. That was enough while the
// only question was "can this come back", but the startup restore offer asks
// a different one -- "did the operator lose this" -- and a row somebody
// killed on purpose reads exactly like one the machine lost in a reboot. So
// every path that ends an agent on purpose says so here, at the moment it
// happens, because afterwards nothing can tell the two apart.
//
// The mark carries the launch of the agent it was about, like the restore
// ledger's does: a session killed, revived and then lost in a crash launched
// a second time, and that second loss is one the operator never chose. A
// table rather than a settings row, because the writers are several
// processes at once -- the board, the CLI, every session's MCP server -- and
// a row per session needs no read-modify-write to stay correct under that.

// Reasons an operator ends a session. Stored as text, so a reason added later
// reads back unchanged in an older build.
const (
	// EndKilled is a kill from the board, the CLI, the MCP tool or an
	// extension, and the kill a restart or an account switch does on the way
	// to a relaunch (which moves the launch, retiring the mark).
	EndKilled = "killed"
	// EndArchived is a session archived while it ran.
	EndArchived = "archived"
	// EndParked is park, which stops the board so unpark can bring it back.
	EndParked = "parked"
)

// SessionEnd is one operator-ended agent: why, and which launch it ended.
type SessionEnd struct {
	Reason string
	// Launched is the LaunchTime of the agent that was ended. A mark whose
	// launch no longer matches the row is about an earlier life.
	Launched time.Time
	At       time.Time
}

// Matches reports whether the mark is about the agent this row now holds.
func (e SessionEnd) Matches(sess Session) bool {
	return e.Reason != "" && e.Launched.Equal(sess.LaunchTime())
}

// RecordEnd marks sess's current agent as ended by the operator.
func (s *Store) RecordEnd(sess Session, reason string) error {
	_, err := s.db.Exec(`INSERT INTO session_ends (session_id, reason, launched, ended_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET reason = excluded.reason, launched = excluded.launched, ended_at = excluded.ended_at`,
		sess.ID, reason, encodeTime(sess.LaunchTime()), encodeTime(time.Now()))
	return err
}

// SessionEnds returns every recorded end by session id.
func (s *Store) SessionEnds() (map[string]SessionEnd, error) {
	rows, err := s.db.Query(`SELECT session_id, reason, launched, ended_at FROM session_ends`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ends := map[string]SessionEnd{}
	for rows.Next() {
		var id, reason string
		var launched, at int64
		if err := rows.Scan(&id, &reason, &launched, &at); err != nil {
			return nil, err
		}
		ends[id] = SessionEnd{Reason: reason, Launched: decodeTime(launched), At: decodeTime(at)}
	}
	return ends, rows.Err()
}
