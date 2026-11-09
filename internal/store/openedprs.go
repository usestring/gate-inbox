package store

import (
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Opened pull requests are the one fact about a session's work worth keeping
// outside the transcript. The history index finds them by reading the
// transcript, and the transcript is not permanent: a restart moves the
// session onto a new conversation and the old file stops being indexed, and
// the CLIs delete their own history on a retention clock. A pull request the
// session opened is still its work after both, so the fact is written once
// here and read back alongside whatever the index currently holds.
//
// Only the identity is stored here, and it is stored per session: this table
// answers "which pull requests are this session's work", which is a fact about
// the session and stays true whatever GitHub later says. What GitHub says
// lives in forge_prs, keyed by the reference rather than the session, and is
// restored as an explicitly unverified value rather than a current one --
// forgestate.go carries why that distinction is what makes storing it safe.

// RecordOpenedPRs remembers that a session opened the pull requests at these
// URLs. Rows the store already has are left alone, so calling it with the
// whole set the index knows is idempotent and costs one transaction. Indexing
// can finish after a handoff; copy that evidence along explicit lineage before
// retention can remove the original session.
func (s *Store) RecordOpenedPRs(sessionID string, urls []string, now time.Time) error {
	if len(urls) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`WITH RECURSIVE custodians(id) AS (
 SELECT ? UNION SELECT successor.id FROM sessions s JOIN custodians c ON s.id = c.id
 JOIN sessions successor ON successor.id = s.replaced_by WHERE s.replaced_by != ''
) INSERT OR IGNORE INTO session_prs (session_id, url, first_seen) SELECT id, ?, ? FROM custodians`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	at := now.UnixMilli()
	for _, url := range urls {
		if url == "" {
			continue
		}
		if _, err := stmt.Exec(sessionID, url, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// OpenedPRs includes work inherited through committed replacements. Original
// rows stay with their session, so retiring an owner never erases provenance.
func (s *Store) OpenedPRs(sessionID string) ([]string, error) {
	rows, err := s.db.Query(`WITH RECURSIVE predecessors(id) AS (
 SELECT ? UNION SELECT s.id FROM sessions s JOIN predecessors p ON s.replaced_by = p.id
)
SELECT url FROM session_prs WHERE session_id IN (SELECT id FROM predecessors)
GROUP BY url ORDER BY MIN(first_seen), url`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var urls []string
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, err
		}
		urls = append(urls, url)
	}
	return urls, rows.Err()
}

func deleteOpenedPRs(tx *sql.Tx, sessionID string) error {
	_, err := tx.Exec(`DELETE FROM session_prs WHERE session_id = ?`, sessionID)
	return err
}

func copyOpenedPRs(tx *sql.Tx, fromID, toID string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO session_prs (session_id, url, first_seen)
 WITH RECURSIVE predecessors(id) AS (
 SELECT ? UNION SELECT s.id FROM sessions s JOIN predecessors p ON s.replaced_by = p.id
) SELECT ?, url, MIN(first_seen) FROM session_prs WHERE session_id IN (SELECT id FROM predecessors) GROUP BY url`, fromID, toID)
	return err
}

var canonicalPR = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+/pull/[1-9][0-9]*$`)

func CanonicalPRURL(url string) (string, error) {
	if len(url) > 2048 || !canonicalPR.MatchString(url) {
		return "", fmt.Errorf("expected a canonical https GitHub pull request URL")
	}
	return strings.ToLower(url), nil
}

type prQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// PROwners returns distinct active custodians, never guessing a handoff from a
// shared URL or migration group. Liveness still needs the caller's pane check.
func (s *Store) PROwners(url string, agentTools []string) ([]Session, error) {
	return prOwners(s.db, url, agentTools)
}

func prOwner(tx *sql.Tx, url string, agentTools []string) (string, error) {
	owners, err := prOwners(tx, url, agentTools)
	if err != nil || len(owners) != 1 {
		return "", err
	}
	return owners[0].ID, nil
}

func prOwners(db prQueryer, url string, agentTools []string) ([]Session, error) {
	url, err := CanonicalPRURL(url)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`WITH RECURSIVE lineage(id) AS (
 SELECT session_id FROM session_prs WHERE url = ? COLLATE NOCASE
 UNION SELECT s.replaced_by FROM sessions s JOIN lineage l ON s.id = l.id WHERE s.replaced_by != ''
 )
 SELECT l.id, s.id, s.tool, s.status, s.archived, s.replaced_by, s.tmux_socket, s.tmux_pane_id,
 EXISTS(SELECT 1 FROM session_prs p WHERE p.session_id = l.id AND p.url = ? COLLATE NOCASE)
 FROM lineage l LEFT JOIN sessions s ON s.id = l.id ORDER BY l.id`, url, url)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := map[string]Session{}
	var seeds []string
	for rows.Next() {
		var id string
		var found, tool, state, replaced, socket, pane sql.NullString
		var archived sql.NullInt64
		var seed bool
		if err := rows.Scan(&id, &found, &tool, &state, &archived, &replaced, &socket, &pane, &seed); err != nil {
			return nil, err
		}
		if !found.Valid {
			return nil, fmt.Errorf("PR custody refers to missing session %s", id)
		}
		sessions[id] = Session{ID: id, Tool: tool.String, Status: state.String, Archived: archived.Int64 != 0,
			ReplacedBy: replaced.String, TmuxSocket: socket.String, TmuxPaneID: pane.String}
		if seed {
			seeds = append(seeds, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	seenOwners := map[string]bool{}
	var owners []Session
	for _, id := range seeds {
		visited := map[string]bool{}
		for sessions[id].ReplacedBy != "" {
			if visited[id] {
				return nil, fmt.Errorf("PR custody has a replacement cycle at %s", id)
			}
			visited[id] = true
			id = sessions[id].ReplacedBy
		}
		sess := sessions[id]
		if !seenOwners[id] && !sess.Archived && sess.Status != "dead" && slices.Contains(agentTools, sess.Tool) {
			seenOwners[id] = true
			owners = append(owners, sess)
		}
	}
	slices.SortFunc(owners, func(a, b Session) int { return strings.Compare(a.ID, b.ID) })
	return owners, nil
}
