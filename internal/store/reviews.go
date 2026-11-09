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
	SessionHead  string
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
		session_head, created_at, updated_at
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
			&triggered, &r.State, &r.SessionID, &r.SessionHead, &created, &updated); err != nil {
			return nil, err
		}
		r.TriggeredAt = decodeTime(triggered)
		r.CreatedAt, r.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetPullRequestReviewState moves every open ask on one pull request to state, recording the
// review session and the head it was started on when one is given. Closed asks are left alone,
// so this never reopens one.
func (s *Store) SetPullRequestReviewState(repo string, number int, state, sessionID, head string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE review_requests
		SET state = ?, `+sessionColumns+`, updated_at = ?
		WHERE repo = ? AND number = ? AND state IN (?, ?)`,
		state, sessionID, head, sessionID, sessionID, now.UnixMilli(), repo, number, ReviewPending,
		ReviewStarted)
	return err
}

// sessionColumns records a review session and the head it was started on, taking sessionID, head,
// sessionID and sessionID. HeadSHA follows the pull request each time an ingest finds it again,
// while session_head stays on the commit the session was asked to review: an empty session changes
// neither, and the session already recorded keeps its head.
const sessionColumns = `session_head = CASE WHEN ? IN ('', session_id) THEN session_head ELSE ? END,
		session_id = CASE WHEN ? = '' THEN session_id ELSE ? END`

// Draft states. A draft starts proposed when an agent writes it and kept when the operator does;
// only kept drafts go out with the review.
const (
	DraftProposed = "proposed"
	DraftKept     = "kept"
	DraftDropped  = "dropped"
)

// Who wrote a draft.
const (
	DraftByAgent    = "agent"
	DraftByOperator = "operator"
)

// ReviewDraft is one inline comment waiting on the operator's review of a pull request. Line and
// Side are GitHub's: the new file's line on RIGHT, the old file's on LEFT.
type ReviewDraft struct {
	ID        int64
	Repo      string
	Number    int
	HeadSHA   string
	Path      string
	Line      int
	Side      string
	Body      string
	Kind      string
	Context   string
	Author    string
	SessionID string
	State     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AddReviewDraft stores a new draft and returns it with its id.
func (s *Store) AddReviewDraft(d ReviewDraft, now time.Time) (ReviewDraft, error) {
	if d.Side == "" {
		d.Side = "RIGHT"
	}
	if d.State == "" {
		d.State = DraftProposed
	}
	res, err := s.db.Exec(`INSERT INTO review_drafts
		(repo, number, head_sha, path, line, side, body, kind, context, author, session_id, state,
		 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Repo, d.Number, d.HeadSHA, d.Path, d.Line, d.Side, d.Body, d.Kind, d.Context, d.Author,
		d.SessionID, d.State, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return ReviewDraft{}, err
	}
	d.ID, err = res.LastInsertId()
	d.CreatedAt, d.UpdatedAt = time.UnixMilli(now.UnixMilli()), time.UnixMilli(now.UnixMilli())
	return d, err
}

// ReviewDrafts is every draft on a pull request that no review has submitted yet, dropped ones
// included, in the order they were written.
func (s *Store) ReviewDrafts(repo string, number int) ([]ReviewDraft, error) {
	rows, err := s.db.Query(`SELECT id, repo, number, head_sha, path, line, side, body, kind,
		context, author, session_id, state, created_at, updated_at
		FROM review_drafts WHERE repo = ? AND number = ? AND submitted_at = 0
		ORDER BY id`, repo, number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewDraft
	for rows.Next() {
		var d ReviewDraft
		var created, updated int64
		if err := rows.Scan(&d.ID, &d.Repo, &d.Number, &d.HeadSHA, &d.Path, &d.Line, &d.Side,
			&d.Body, &d.Kind, &d.Context, &d.Author, &d.SessionID, &d.State, &created, &updated); err != nil {
			return nil, err
		}
		d.CreatedAt, d.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetReviewDraft moves a draft to state, and replaces its body unless body is empty.
func (s *Store) SetReviewDraft(id int64, state, body string, now time.Time) error {
	res, err := s.db.Exec(`UPDATE review_drafts
		SET state = ?, body = CASE WHEN ? = '' THEN body ELSE ? END, updated_at = ?
		WHERE id = ? AND submitted_at = 0`, state, body, body, now.UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// FinishReviewDrafts closes the drafts a submitted review was written from, so a later ask on the
// same pull request starts without them. A draft added or changed while the review was on its way
// stays open, since what reached GitHub is not what it now says.
func (s *Store) FinishReviewDrafts(drafts []ReviewDraft, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range drafts {
		if _, err := tx.Exec(`UPDATE review_drafts SET submitted_at = ?, updated_at = ?
			WHERE id = ? AND updated_at = ? AND submitted_at = 0`,
			now.UnixMilli(), now.UnixMilli(), d.ID, d.UpdatedAt.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AttachReviewSession records a session prepared for one pull request's open asks, and the head
// it was started on, without moving their state, so a prepared ask still counts as waiting until
// the operator opens it.
func (s *Store) AttachReviewSession(repo string, number int, sessionID, head string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE review_requests SET `+sessionColumns+`, updated_at = ?
		WHERE repo = ? AND number = ? AND state IN (?, ?)`,
		sessionID, head, sessionID, sessionID, now.UnixMilli(), repo, number, ReviewPending, ReviewStarted)
	return err
}
