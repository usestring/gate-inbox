package store

import (
	"errors"
	"strings"
	"time"
)

// The dialog answer ledger.
//
// Claude Code hands an AskUserQuestion answer to its auto-mode classifier as
// the user's input, so an answer Gate Inbox keys into a child on a parent's
// behalf reads to that child as its user speaking. The ledger is how anything
// downstream can tell: every keyed answer is a row naming who asked for it and
// whether it was relayed from a user's own dialog or chosen by an agent. The
// row is written before the first keystroke, because the child's PostToolUse
// hook fires the moment the dialog submits and looks for it then.

// AnswerMode says whose answer a ledger row carries.
type AnswerMode string

const (
	// AnswerByAgent is an answer the parent agent chose.
	AnswerByAgent AnswerMode = "agent"
	// AnswerRelayedUser is an answer matched word for word to the answer the
	// user gave the same question in the parent's own dialog.
	AnswerRelayedUser AnswerMode = "relayed_user"
)

// AnswerState is how far a ledger row's keystrokes got.
type AnswerState string

const (
	AnswerPending AnswerState = "pending"
	AnswerKeyed   AnswerState = "keyed"
	AnswerFailed  AnswerState = "failed"
)

// DialogAnswer is one row of the ledger.
type DialogAnswer struct {
	ID                int64
	TargetSession     string
	TargetToolUseID   string
	QuestionHash      string
	Answer            string
	BySession         string
	Mode              AnswerMode
	EvidenceToolUseID string
	CreatedAt         time.Time
	State             AnswerState
}

// ErrEvidenceUsed is a relay citing a parent dialog an earlier answer already
// cited.
var ErrEvidenceUsed = errors.New("that answer of the user's has already been relayed once")

// RecordAnswer writes row as pending and returns its id. A second row citing
// the same evidence for the same question is refused with ErrEvidenceUsed.
func (s *Store) RecordAnswer(row DialogAnswer) (int64, error) {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	res, err := s.db.Exec(`INSERT INTO dialog_answers
		(target_session, target_tool_use_id, question_hash, answer, by_session, mode, evidence_tool_use_id, created_at, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.TargetSession, row.TargetToolUseID, row.QuestionHash, row.Answer, row.BySession,
		string(row.Mode), row.EvidenceToolUseID, row.CreatedAt.UnixMilli(), string(AnswerPending))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrEvidenceUsed
		}
		return 0, err
	}
	return res.LastInsertId()
}

// MarkAnswer records how a row's keystrokes ended.
func (s *Store) MarkAnswer(id int64, state AnswerState) error {
	_, err := s.db.Exec(`UPDATE dialog_answers SET state = ? WHERE id = ?`, string(state), id)
	return err
}

const dialogAnswerColumns = `id, target_session, target_tool_use_id, question_hash, answer, by_session, mode,
	evidence_tool_use_id, created_at, state`

func scanDialogAnswer(row interface{ Scan(...any) error }) (DialogAnswer, error) {
	var (
		answer      DialogAnswer
		mode, state string
		created     int64
	)
	err := row.Scan(&answer.ID, &answer.TargetSession, &answer.TargetToolUseID, &answer.QuestionHash,
		&answer.Answer, &answer.BySession, &mode, &answer.EvidenceToolUseID, &created, &state)
	answer.Mode, answer.State = AnswerMode(mode), AnswerState(state)
	answer.CreatedAt = time.UnixMilli(created)
	return answer, err
}

// AnswersFor is every ledger row keyed into target's dialog toolUseID, oldest
// first. An empty toolUseID matches rows whose call id was not known.
func (s *Store) AnswersFor(target, toolUseID string) ([]DialogAnswer, error) {
	rows, err := s.db.Query(`SELECT `+dialogAnswerColumns+` FROM dialog_answers
		WHERE target_session = ? AND target_tool_use_id = ? ORDER BY id`, target, toolUseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DialogAnswer
	for rows.Next() {
		answer, err := scanDialogAnswer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, answer)
	}
	return out, rows.Err()
}

// EvidenceUsed reports whether a row already cites the user's answer to
// questionHash in the parent dialog toolUseID as its evidence.
func (s *Store) EvidenceUsed(toolUseID, questionHash string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM dialog_answers WHERE evidence_tool_use_id = ? AND question_hash = ?`,
		toolUseID, questionHash).Scan(&n)
	return n > 0, err
}

// AgentAnswered reports whether Gate Inbox keyed an agent's answer into
// session's dialog toolUseID: that dialog's answer is not the user's. A row
// whose keystrokes failed still counts, since some of them may have landed.
func (s *Store) AgentAnswered(session, toolUseID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM dialog_answers
		WHERE target_session = ? AND target_tool_use_id = ? AND mode = ?`,
		session, toolUseID, string(AnswerByAgent)).Scan(&n)
	return n > 0, err
}
