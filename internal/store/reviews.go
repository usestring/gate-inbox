package store

import (
	"database/sql"
	"time"
)

// The review queue: one row per ask for the operator's review, keyed by the ask itself -- the
// pull request, which trigger, and the id of the request, labelling or comment that asked.
//
// Keying on the ask rather than the pull request is what makes ingest idempotent without making it
// forgetful. The same ask found again updates its row in place and never brings back one the
// operator dismissed or submitted; a new ask on the same pull request is a new key and queues again.

// Review states.
const (
	ReviewPending   = "pending"
	ReviewStarted   = "started"
	ReviewDismissed = "dismissed"
	ReviewSubmitted = "submitted"
)

// ReviewRequest is one stored ask. It mirrors forge.ReviewRequest for the same reason StoredPR
// mirrors forge.PR: store cannot import forge.
type ReviewRequest struct {
	Repo         string
	Number       int
	Trigger      string
	TriggerID    string
	Title        string
	Author       string
	URL          string
	HeadSHA      string
	Draft        bool
	Additions    int
	Deletions    int
	ChangedFiles int
	Checks       string
	TriggeredAt  time.Time
	State        string
	SessionID    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Open reports whether the ask is still the operator's to act on.
func (r ReviewRequest) Open() bool { return r.State == ReviewPending || r.State == ReviewStarted }

// UpsertReviewRequests records what an ingest found and returns the asks it had not seen before.
//
// An ask already stored keeps its state: the pull request's details are refreshed, but a dismissed
// or submitted ask stays that way however often the search finds it again.
func (s *Store) UpsertReviewRequests(found []ReviewRequest, now time.Time) ([]ReviewRequest, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var added []ReviewRequest
	at := now.UnixMilli()
	for _, r := range found {
		var state string
		err := tx.QueryRow(`SELECT state FROM review_requests
			WHERE repo = ? AND number = ? AND trigger = ? AND trigger_id = ?`,
			r.Repo, r.Number, r.Trigger, r.TriggerID).Scan(&state)
		switch {
		case err == sql.ErrNoRows:
			if _, err := tx.Exec(`INSERT INTO review_requests
				(repo, number, trigger, trigger_id, title, author, url, head_sha, draft, additions,
				 deletions, changed, checks, triggered_at, state, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				r.Repo, r.Number, r.Trigger, r.TriggerID, r.Title, r.Author, r.URL, r.HeadSHA,
				r.Draft, r.Additions, r.Deletions, r.ChangedFiles, r.Checks,
				encodeTime(r.TriggeredAt), ReviewPending, at, at); err != nil {
				return nil, err
			}
			r.State, r.CreatedAt, r.UpdatedAt = ReviewPending, now, now
			added = append(added, r)
		case err != nil:
			return nil, err
		default:
			if _, err := tx.Exec(`UPDATE review_requests SET title = ?, author = ?, url = ?,
				head_sha = ?, draft = ?, additions = ?, deletions = ?, changed = ?, checks = ?,
				updated_at = ?
				WHERE repo = ? AND number = ? AND trigger = ? AND trigger_id = ?`,
				r.Title, r.Author, r.URL, r.HeadSHA, r.Draft, r.Additions, r.Deletions,
				r.ChangedFiles, r.Checks, at, r.Repo, r.Number, r.Trigger, r.TriggerID); err != nil {
				return nil, err
			}
		}
	}
	return added, tx.Commit()
}

// OpenReviewRequests is every ask still pending or started, oldest ask first.
func (s *Store) OpenReviewRequests() ([]ReviewRequest, error) {
	rows, err := s.db.Query(`SELECT repo, number, trigger, trigger_id, title, author, url,
		head_sha, draft, additions, deletions, changed, checks, triggered_at, state, session_id,
		created_at, updated_at
		FROM review_requests WHERE state IN (?, ?)
		ORDER BY triggered_at, repo, number`, ReviewPending, ReviewStarted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewRequest
	for rows.Next() {
		var r ReviewRequest
		var triggered, created, updated int64
		if err := rows.Scan(&r.Repo, &r.Number, &r.Trigger, &r.TriggerID, &r.Title, &r.Author,
			&r.URL, &r.HeadSHA, &r.Draft, &r.Additions, &r.Deletions, &r.ChangedFiles, &r.Checks,
			&triggered, &r.State, &r.SessionID, &created, &updated); err != nil {
			return nil, err
		}
		r.TriggeredAt = decodeTime(triggered)
		r.CreatedAt, r.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetPullRequestReviewState moves every open ask on one pull request to state, recording the
// review session when one is given. Closed asks are left alone, so this never reopens one.
func (s *Store) SetPullRequestReviewState(repo string, number int, state, sessionID string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE review_requests
		SET state = ?, session_id = CASE WHEN ? = '' THEN session_id ELSE ? END, updated_at = ?
		WHERE repo = ? AND number = ? AND state IN (?, ?)`,
		state, sessionID, sessionID, now.UnixMilli(), repo, number, ReviewPending, ReviewStarted)
	return err
}
