package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

type CIObservation struct {
	URL        string
	HeadSHA    string
	Signature  string
	At         time.Time
	Open       bool
	Passing    bool
	Body       string
	AgentTools []string
}

type ciFeedbackState struct {
	At        time.Time
	Attempts  int
	Seen      []string
	Escalated bool
}

func (s *Store) ObserveCI(obs CIObservation) (int64, error) {
	if obs.URL == "" || obs.HeadSHA == "" || obs.At.IsZero() {
		return 0, nil
	}
	// Queueing and spending a round must commit together, including across board restarts.
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	key := "ci-feedback:v1:" + obs.URL
	var raw string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var state ciFeedbackState
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return 0, fmt.Errorf("read CI feedback state: %w", err)
		}
	}
	if !obs.At.After(state.At) {
		return 0, nil
	}

	owner, err := ciOwner(tx, obs.URL, obs.AgentTools)
	if err != nil {
		return 0, err
	}
	if owner == "" {
		if err := retireCI(tx, key, obs.At); err != nil {
			return 0, err
		}
		return 0, tx.Commit()
	}
	state.At = obs.At
	if obs.Passing {
		state.Attempts, state.Seen, state.Escalated = 0, nil, false
	}
	seen := slices.Contains(state.Seen, obs.Signature)
	var retry bool
	if seen && !state.Escalated && obs.Signature != "" && obs.Open {
		retry, err = retiredCIFeedback(tx, key+":"+obs.Signature)
		if err != nil {
			return 0, err
		}
	}
	var id int64
	if obs.Signature == "" || !obs.Open {
		if err := retireCI(tx, key, obs.At); err != nil {
			return 0, err
		}
	} else if !state.Escalated && (!seen || retry) {
		if err := retireCI(tx, key, obs.At); err != nil {
			return 0, err
		}
		body := obs.Body
		if state.Attempts >= 2 && !retry {
			body = fmt.Sprintf("CI still fails for %s at commit %s after two automatic feedback rounds. Automatic repair feedback is paused until CI passes. Report the remaining failure and ask the operator how to proceed; do not start another automatic repair.\n\n%s", obs.URL, obs.HeadSHA, obs.Body)
			state.Escalated = true
		} else {
			if !retry {
				state.Attempts++
			}
			body = fmt.Sprintf("CI feedback round %d of 2. Before acting, verify the PR is still open at the reported commit and these checks still fail. Investigate and repair only within the session's existing task and permissions. Follow the repository's PR claim/ownership procedure before editing; if another worker owns the PR, leave it to that worker.\n\n%s", state.Attempts, body)
		}
		limits := DefaultInboxLimits
		if retry {
			// The durable observation state deduplicates this retry; its earlier packet was never delivered.
			limits.DedupeWindow = 0
		}
		id, err = s.insertMessage(tx, InboxMessage{
			SessionID: owner, SenderID: SystemSenderID, SenderName: "Gate Inbox CI",
			Subject: key, Fingerprint: key + ":" + obs.Signature, Body: body, SentAt: obs.At,
		}, limits)
		if err != nil {
			return 0, err
		}
		if !seen {
			state.Seen = append(state.Seen, obs.Signature)
		}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(encoded)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func retiredCIFeedback(tx *sql.Tx, fingerprint string) (bool, error) {
	var retired bool
	err := tx.QueryRow(`SELECT dropped_at != 0 AND claimed_at = 0 AND drop_reason = 'ci-state-changed'
FROM session_inbox WHERE sender_id = ? AND fingerprint = ? ORDER BY id DESC LIMIT 1`,
		SystemSenderID, fingerprint).Scan(&retired)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return retired, err
}

func ciOwner(tx *sql.Tx, url string, agentTools []string) (string, error) {
	rows, err := tx.Query(`SELECT s.id, s.tool FROM session_prs p JOIN sessions s ON s.id = p.session_id
WHERE p.url = ? AND s.archived = 0 AND s.status != 'dead' AND s.replaced_by = ''`, url)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var id, tool string
		if err := rows.Scan(&id, &tool); err != nil {
			return "", err
		}
		if slices.Contains(agentTools, tool) {
			owners = append(owners, id)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(owners) != 1 {
		return "", nil
	}
	return owners[0], nil
}

func retireCI(tx *sql.Tx, subject string, at time.Time) error {
	_, err := tx.Exec(`UPDATE session_inbox SET delivered_at = ?, dropped_at = ?, drop_reason = 'ci-state-changed'
WHERE sender_id = ? AND subject = ? AND delivered_at = 0 AND claimed_at = 0`,
		encodeTime(at), encodeTime(at), SystemSenderID, subject)
	return err
}

func (s *Store) CancelCIFeedback(at time.Time) error {
	_, err := s.db.Exec(`UPDATE session_inbox SET delivered_at = ?, dropped_at = ?, drop_reason = 'ci-feedback-disabled'
WHERE sender_id = ? AND subject LIKE 'ci-feedback:v1:%' AND delivered_at = 0 AND claimed_at = 0`,
		encodeTime(at), encodeTime(at), SystemSenderID)
	return err
}
