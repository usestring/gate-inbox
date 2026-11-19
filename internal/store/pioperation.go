package store

import "strconv"

// PiOperationRule is the stable name reported for a local permission decision.
type PiOperationRule string

const (
	PiReadOnly          PiOperationRule = "read_only"
	PiLinearIssueCreate PiOperationRule = "linear_issue_create"
	PiPullRequestCreate PiOperationRule = "pull_request_create"
	PiRepositoryPush    PiOperationRule = "repository_push"
)

const piOperationPrefix = "pi_operation_rule."

// CountPiOperationRules records the rules applied to one call atomically.
// Several hooks can decide tool calls concurrently on the same board.
func (s *Store) CountPiOperationRules(rules []PiOperationRule) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, rule := range rules {
		if _, err := tx.Exec(`
INSERT INTO settings (key, value) VALUES (?, '1')
ON CONFLICT(key) DO UPDATE SET value = CAST(settings.value AS INTEGER) + 1`, piOperationPrefix+string(rule)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PiOperationRuleCounts reports all the supported counters, including rules
// that have never been used. It records no command or tool arguments.
func (s *Store) PiOperationRuleCounts() (map[PiOperationRule]int64, error) {
	counts := make(map[PiOperationRule]int64)
	for _, rule := range []PiOperationRule{PiReadOnly, PiLinearIssueCreate, PiPullRequestCreate, PiRepositoryPush} {
		value, err := s.Setting(piOperationPrefix + string(rule))
		if err != nil {
			return nil, err
		}
		var count int64
		if value != "" {
			count, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, err
			}
		}
		counts[rule] = count
	}
	return counts, nil
}
