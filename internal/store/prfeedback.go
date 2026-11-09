package store

import (
	"fmt"
	"time"
)

type PRFeedbackKind string

const (
	PRFeedbackReview   PRFeedbackKind = "review"
	PRFeedbackConflict PRFeedbackKind = "conflict"
)

func (s *Store) ObservePRFeedback(kind PRFeedbackKind, obs FeedbackObservation) (int64, error) {
	var resolved string
	switch kind {
	case PRFeedbackReview:
		resolved = "the review requests and threads are resolved"
	case PRFeedbackConflict:
		resolved = "GitHub confirms the PR is mergeable"
	default:
		return 0, fmt.Errorf("unknown PR feedback kind %q", kind)
	}
	return s.observeFeedback(obs, feedbackPolicy{"pr-feedback:v1:" + string(kind) + ":", "PR " + string(kind), resolved})
}

func (s *Store) CancelPRFeedback(at time.Time) error {
	_, err := s.db.Exec(`UPDATE session_inbox SET delivered_at = ?, dropped_at = ?, drop_reason = 'pr-feedback-disabled'
WHERE sender_id = ? AND subject LIKE 'pr-feedback:v1:%' AND delivered_at = 0 AND claimed_at = 0`,
		encodeTime(at), encodeTime(at), SystemSenderID)
	return err
}
