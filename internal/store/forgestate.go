package store

import (
	"time"
)

// Resolved pull request and ticket state, kept across restarts.
//
// This is a deliberate reversal of the rule stated in openedprs.go, which kept only the identity of
// a pull request on the grounds that its state is GitHub's to say and should be fetched live. That
// held while the process was the only cache: every start asked about every reference on the board at
// once, which is the largest single burst of quota the inbox spends and it lands in the first
// seconds of every restart — exactly when the operator is watching an empty work column.
//
// What makes storing it safe is that a restored row is not treated as current. The tracker seeds its
// cache from here and leaves the row unverified, so it is due immediately and is drawn as a
// remembered value with the time it was taken, not as a live one. The board says "as of 09:12"
// rather than claiming a merge that may have happened since. Being late is fine; being confidently
// wrong is not.
//
// Rows are pruned on load rather than swept on a timer: a week-old answer is not worth restoring
// even marked as stale, and load is the only moment the cost of keeping it is being paid.

// forgeRetention is how old a stored answer may be and still be worth restoring.
//
// Long enough to cover a laptop closed over a weekend, which is the case this exists for. Past that
// the reference is either finished or forgotten, and asking again is both cheap and more honest than
// showing a week-old badge.
const forgeRetention = 7 * 24 * time.Hour

// StoredPR is one resolved pull request as the tracker last saw it.
//
// The fields mirror forge.PR rather than importing it: store sits underneath the whole application
// and cannot depend on a package that depends on it. The cost is this struct and the two conversions
// at the boundary; the benefit is that the schema is visible in one place next to the table that
// holds it.
type StoredPR struct {
	Key           string
	Repo          string
	Number        int
	Title         string
	State         string
	Checks        string
	Review        string
	URL           string
	Mergeable     bool
	HeadRef       string
	FailingChecks int
	FetchedAt     time.Time
}

// StoredTicket is one resolved ticket as the tracker last saw it.
type StoredTicket struct {
	Key        string
	Identifier string
	Title      string
	State      string
	StateType  string
	Assignee   string
	URL        string
	FetchedAt  time.Time
}

// SaveForgeState writes what a refresh resolved, replacing whatever was there for those keys.
//
// Replace rather than merge: the tracker holds the whole truth about a reference, so a partial
// update would only be a way for two writers to disagree. One transaction because a refresh resolves
// a batch at a time and this runs on the refresh goroutine, behind the network call that already
// dominates it.
func (s *Store) SaveForgeState(prs []StoredPR, tickets []StoredTicket) error {
	if len(prs) == 0 && len(tickets) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(prs) > 0 {
		stmt, err := tx.Prepare(`INSERT OR REPLACE INTO forge_prs
			(key, repo, number, title, state, checks, review, url, mergeable, head_ref, failing_checks, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, pr := range prs {
			if _, err := stmt.Exec(pr.Key, pr.Repo, pr.Number, pr.Title, pr.State, pr.Checks,
				pr.Review, pr.URL, pr.Mergeable, pr.HeadRef, pr.FailingChecks,
				pr.FetchedAt.UnixMilli()); err != nil {
				return err
			}
		}
	}

	if len(tickets) > 0 {
		stmt, err := tx.Prepare(`INSERT OR REPLACE INTO forge_tickets
			(key, identifier, title, state, state_type, assignee, url, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, ticket := range tickets {
			if _, err := stmt.Exec(ticket.Key, ticket.Identifier, ticket.Title, ticket.State,
				ticket.StateType, ticket.Assignee, ticket.URL,
				ticket.FetchedAt.UnixMilli()); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ForgeState is everything still worth restoring, and drops everything that is not.
//
// The prune runs here because this is called once at startup: a sweep on its own clock would be a
// second thing to schedule for a table nobody reads between restarts.
func (s *Store) ForgeState(now time.Time) ([]StoredPR, []StoredTicket, error) {
	cutoff := now.Add(-forgeRetention).UnixMilli()
	if _, err := s.db.Exec(`DELETE FROM forge_prs WHERE fetched_at < ?`, cutoff); err != nil {
		return nil, nil, err
	}
	if _, err := s.db.Exec(`DELETE FROM forge_tickets WHERE fetched_at < ?`, cutoff); err != nil {
		return nil, nil, err
	}

	prRows, err := s.db.Query(`SELECT key, repo, number, title, state, checks, review, url,
		mergeable, head_ref, failing_checks, fetched_at FROM forge_prs`)
	if err != nil {
		return nil, nil, err
	}
	defer prRows.Close()
	var prs []StoredPR
	for prRows.Next() {
		var pr StoredPR
		var at int64
		if err := prRows.Scan(&pr.Key, &pr.Repo, &pr.Number, &pr.Title, &pr.State, &pr.Checks,
			&pr.Review, &pr.URL, &pr.Mergeable, &pr.HeadRef, &pr.FailingChecks, &at); err != nil {
			return nil, nil, err
		}
		pr.FetchedAt = time.UnixMilli(at)
		prs = append(prs, pr)
	}
	if err := prRows.Err(); err != nil {
		return nil, nil, err
	}

	ticketRows, err := s.db.Query(`SELECT key, identifier, title, state, state_type, assignee, url,
		fetched_at FROM forge_tickets`)
	if err != nil {
		return nil, nil, err
	}
	defer ticketRows.Close()
	var tickets []StoredTicket
	for ticketRows.Next() {
		var ticket StoredTicket
		var at int64
		if err := ticketRows.Scan(&ticket.Key, &ticket.Identifier, &ticket.Title, &ticket.State,
			&ticket.StateType, &ticket.Assignee, &ticket.URL, &at); err != nil {
			return nil, nil, err
		}
		ticket.FetchedAt = time.UnixMilli(at)
		tickets = append(tickets, ticket)
	}
	return prs, tickets, ticketRows.Err()
}
