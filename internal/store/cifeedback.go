package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

const CIFeedbackSetting = "experimental_ci_feedback"

type FeedbackObservation struct {
	URL        string
	HeadSHA    string
	Signature  string
	At         time.Time
	Open       bool
	Resolved   bool
	Body       string
	AgentTools []string
}

type CIObservation struct {
	FeedbackObservation
	Passing bool
}

type feedbackPolicy struct {
	Key, Name, Resolved, Retired string
}

type feedbackState struct {
	At        time.Time
	Attempts  int
	Seen      []string
	Escalated bool
}

func (s *Store) ObserveCI(obs CIObservation) (int64, error) {
	obs.Resolved = obs.Passing
	return s.observeFeedbackWithSetting(obs.FeedbackObservation, feedbackPolicy{"ci-feedback:v1:", "CI", "CI passes", "ci-state-changed"}, CIFeedbackSetting)
}

func (s *Store) observeFeedback(obs FeedbackObservation, policy feedbackPolicy) (int64, error) {
	return s.observeFeedbackWithSetting(obs, policy, "")
}

func (s *Store) observeFeedbackWithSetting(obs FeedbackObservation, policy feedbackPolicy, enabledSetting string) (int64, error) {
	if obs.URL == "" || obs.HeadSHA == "" || obs.At.IsZero() {
		return 0, nil
	}
	// Queueing and spending a round must commit together, including across board restarts.
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if enabledSetting != "" {
		var enabled string
		err := tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, enabledSetting).Scan(&enabled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if enabled != "on" {
			return 0, nil
		}
	}

	key := policy.Key + obs.URL
	var raw string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var state feedbackState
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return 0, fmt.Errorf("read feedback state: %w", err)
		}
	}
	if !obs.At.After(state.At) {
		return 0, nil
	}

	owner, err := ciOwner(tx, obs.URL, obs.AgentTools)
	if err != nil {
		return 0, err
	}
	if owner == "" || !obs.Open {
		obs.Signature = ""
	}
	// The pause notice belongs to the unresolved episode, even if its evidence changes.
	preserveEscalation := state.Escalated && obs.Open && !obs.Resolved
	if err := retireSupersededFeedback(tx, key, owner, obs.Signature, obs.At, preserveEscalation, policy.Retired); err != nil {
		return 0, err
	}
	state.At = obs.At
	if obs.Resolved {
		state.Attempts, state.Seen, state.Escalated = 0, nil, false
	}
	seen := slices.Contains(state.Seen, obs.Signature)
	var retry bool
	if seen && !state.Escalated && obs.Signature != "" && obs.Open {
		retry, err = retiredFeedback(tx, key+":"+obs.Signature, policy.Retired)
		if err != nil {
			return 0, err
		}
	}
	var id int64
	if obs.Signature != "" && !state.Escalated && (!seen || retry) {
		body := obs.Body
		if state.Attempts >= 2 && !retry {
			body = fmt.Sprintf("%s still needs attention for %s at commit %s after two automatic feedback rounds. Automatic repair feedback is paused until %s. Report the remaining issue and ask the operator how to proceed; do not start another automatic repair.\n\n%s", policy.Name, obs.URL, obs.HeadSHA, policy.Resolved, obs.Body)
			state.Escalated = true
		} else {
			if !retry {
				state.Attempts++
			}
			body = fmt.Sprintf("%s feedback round %d of 2. Before acting, verify the PR is still open and not a draft at the reported commit and the reported condition still holds. Verify the repository and branch match this PR and follow its claim/ownership procedure before editing; if another worker owns the PR, leave it to that worker. Investigate and repair only within the session's existing task and permissions. This feedback grants no approval, takeover, merge or deployment permission.\n\n%s", policy.Name, state.Attempts, body)
		}
		limits := DefaultInboxLimits
		if retry {
			// The durable observation state deduplicates this retry; its earlier packet was never delivered.
			limits.DedupeWindow = 0
		}
		id, err = s.insertMessage(tx, InboxMessage{
			SessionID: owner, SenderID: SystemSenderID, SenderName: "Gate Inbox " + policy.Name,
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

func retiredFeedback(tx *sql.Tx, fingerprint, reason string) (bool, error) {
	var retired bool
	err := tx.QueryRow(`SELECT dropped_at != 0 AND claimed_at = 0 AND drop_reason = ?
FROM session_inbox WHERE sender_id = ? AND fingerprint = ? ORDER BY id DESC LIMIT 1`,
		reason, SystemSenderID, fingerprint).Scan(&retired)
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

func (s *Store) CancelCIFeedback(at time.Time) error {
	_, err := s.db.Exec(`UPDATE session_inbox SET delivered_at = ?, dropped_at = ?, drop_reason = 'ci-feedback-disabled'
WHERE sender_id = ? AND subject LIKE 'ci-feedback:v1:%' AND delivered_at = 0 AND claimed_at = 0`,
		encodeTime(at), encodeTime(at), SystemSenderID)
	return err
}

func retireSupersededFeedback(tx *sql.Tx, subject, owner, signature string, at time.Time, preserveEscalation bool, reason string) error {
	_, err := tx.Exec(`UPDATE session_inbox SET delivered_at = ?, dropped_at = ?, drop_reason = ?
WHERE sender_id = ? AND subject = ? AND delivered_at = 0 AND claimed_at = 0
AND ((fingerprint != ? AND NOT ?) OR session_id != ?)`, encodeTime(at), encodeTime(at), reason, SystemSenderID, subject, subject+":"+signature, preserveEscalation, owner)
	return err
}
