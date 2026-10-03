package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Permissions a parent granted its child.
//
// The settings file a granted session launches with is what Claude Code
// reads; this table is what Gate Inbox reads. It is the record of who granted
// what to whom on whose approval and until when, it is what the board shows,
// and it is what the settings file is rebuilt from on a revoke or an expiry, so
// a revoked grant cannot survive in the file because nobody remembered to take
// it out.

// PermissionGrant is one row: a permission in force on a session, or one that
// was revoked.
type PermissionGrant struct {
	ID        int64
	SessionID string
	Kind      string
	Value     string
	// GrantedBy is the parent that granted it.
	GrantedBy string
	// EvidenceToolUseID is the parent's own AskUserQuestion call its user
	// answered to approve the grant, and QuestionHash the question in it: one
	// dialog can ask several, each approving its own grant.
	EvidenceToolUseID string
	QuestionHash      string
	CreatedAt         time.Time
	// ExpiresAt is when the grant lapses; the board revokes it then.
	ExpiresAt time.Time
	RevokedAt time.Time
	RevokedBy string
}

// Active reports whether the grant is still in force.
func (g PermissionGrant) Active() bool { return g.RevokedAt.IsZero() }

// GrantExpired is the RevokedBy of a grant the board revoked because its time
// ran out.
const GrantExpired = "expired"

// ErrGrantExists is a grant already in force on the session.
var ErrGrantExists = errors.New("that permission is already granted to the session")

const grantColumns = `id, session_id, kind, value, by_session, evidence_tool_use_id, question_hash,
	created_at, expires_at, revoked_at, revoked_by`

// RecordGrant writes row as in force and returns its id. The same kind and
// value already in force on the session is refused with ErrGrantExists.
func (s *Store) RecordGrant(row PermissionGrant) (int64, error) {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	res, err := s.db.Exec(`INSERT INTO permission_grants
		(session_id, kind, value, by_session, evidence_tool_use_id, question_hash, created_at, expires_at,
		 revoked_at, revoked_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, '')`,
		row.SessionID, row.Kind, row.Value, row.GrantedBy, row.EvidenceToolUseID, row.QuestionHash,
		row.CreatedAt.UnixMilli(), grantMilli(row.ExpiresAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrGrantExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

// RevokeGrant marks every in-force grant on session matching kind and value
// revoked, and reports how many it marked.
func (s *Store) RevokeGrant(session, kind, value, by string) (int64, error) {
	res, err := s.db.Exec(`UPDATE permission_grants SET revoked_at = ?, revoked_by = ?
		WHERE session_id = ? AND kind = ? AND value = ? AND revoked_at = 0`,
		time.Now().UnixMilli(), by, session, kind, value)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Grants is every grant on session, oldest first; active limits it to the
// ones in force.
func (s *Store) Grants(session string, active bool) ([]PermissionGrant, error) {
	query := `SELECT ` + grantColumns + ` FROM permission_grants WHERE session_id = ?`
	if active {
		query += ` AND revoked_at = 0`
	}
	return s.queryGrants(query+` ORDER BY id`, session)
}

// ActiveGrants is every grant in force, by session, for the board and the
// session list.
func (s *Store) ActiveGrants() (map[string][]PermissionGrant, error) {
	rows, err := s.queryGrants(`SELECT ` + grantColumns + ` FROM permission_grants
		WHERE revoked_at = 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := map[string][]PermissionGrant{}
	for _, g := range rows {
		out[g.SessionID] = append(out[g.SessionID], g)
	}
	return out, nil
}

// ExpiredGrants is every grant still in force whose time ran out by now.
func (s *Store) ExpiredGrants(now time.Time) ([]PermissionGrant, error) {
	return s.queryGrants(`SELECT `+grantColumns+` FROM permission_grants
		WHERE revoked_at = 0 AND expires_at != 0 AND expires_at <= ? ORDER BY id`, now.UnixMilli())
}

func (s *Store) queryGrants(query string, args ...any) ([]PermissionGrant, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PermissionGrant
	for rows.Next() {
		var g PermissionGrant
		var created, expires, revoked int64
		if err := rows.Scan(&g.ID, &g.SessionID, &g.Kind, &g.Value, &g.GrantedBy, &g.EvidenceToolUseID,
			&g.QuestionHash, &created, &expires, &revoked, &g.RevokedBy); err != nil {
			return nil, err
		}
		g.CreatedAt = time.UnixMilli(created)
		g.ExpiresAt = grantTime(expires)
		g.RevokedAt = grantTime(revoked)
		out = append(out, g)
	}
	return out, rows.Err()
}

// GrantEvidenceUsed reports whether a grant already cited the question
// questionHash in the parent dialog toolUseID, in force or since revoked: a
// user's approval settles one grant.
func (s *Store) GrantEvidenceUsed(toolUseID, questionHash string) (bool, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM permission_grants WHERE evidence_tool_use_id = ? AND question_hash = ? LIMIT 1`,
		toolUseID, questionHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// grantMilli and grantTime store a time that may be unset as zero.
func grantMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func grantTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
