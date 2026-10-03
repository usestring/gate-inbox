package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// The verified parent channel.
//
// A message typed into a session's prompt reads to it as its user's turn, and
// the words around it are all a sender could imitate. So Gate Inbox seals each
// agent message it types with a key only this store holds (see parentseal),
// and the recipient's own UserPromptSubmit hook checks the seal here and says
// who the message is from. Three tables carry it:
//
//   - the sealing keys are not here: see parentseal/keys.go for why they
//     live in a directory every managed session's sandbox is denied.
//   - seal_uses is every sealed message a hook has checked, so each verifies
//     once: a seal copied off a pane into another prompt is a replay.
//   - relay_attestations is a user's answer Gate Inbox found in a parent's own
//     transcript and attached to one message to its child, under a one-time
//     nonce the child's hook spends.

// SpendSeal records that session's hook checked the seal on messageID, and
// reports false when it already had: the seal is being replayed.
func (s *Store) SpendSeal(messageID int64, session string, at time.Time) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO seal_uses (message_id, session_id, used_at) VALUES (?, ?, ?)`,
		messageID, session, at.UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Attestation is a user's answer, found in a parent's own transcript, that
// Gate Inbox attached to one message to that parent's child.
type Attestation struct {
	Nonce             string
	MessageID         int64
	TargetSession     string
	BySession         string
	EvidenceToolUseID string
	Header            string
	Question          string
	Options           []string
	Answer            string
	AnsweredAt        time.Time
	CreatedAt         time.Time
	SpentAt           time.Time
}

// RecordAttestation stores a.
func (s *Store) RecordAttestation(a Attestation) error {
	options, err := json.Marshal(a.Options)
	if err != nil {
		return err
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	_, err = s.db.Exec(`INSERT INTO relay_attestations
		(nonce, message_id, target_session, by_session, evidence_tool_use_id, header, question, options, answer,
		 answered_at, created_at, spent_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		a.Nonce, a.MessageID, a.TargetSession, a.BySession, a.EvidenceToolUseID, a.Header, a.Question,
		string(options), a.Answer, a.AnsweredAt.UnixMilli(), a.CreatedAt.UnixMilli())
	return err
}

// AttestationFor is the attestation attached to messageID, if any.
func (s *Store) AttestationFor(messageID int64) (Attestation, bool, error) {
	var (
		a                        Attestation
		options                  string
		answered, created, spent int64
	)
	err := s.db.QueryRow(`SELECT nonce, message_id, target_session, by_session, evidence_tool_use_id, header,
		question, options, answer, answered_at, created_at, spent_at
		FROM relay_attestations WHERE message_id = ?`, messageID).Scan(
		&a.Nonce, &a.MessageID, &a.TargetSession, &a.BySession, &a.EvidenceToolUseID, &a.Header, &a.Question,
		&options, &a.Answer, &answered, &created, &spent)
	if errors.Is(err, sql.ErrNoRows) {
		return Attestation{}, false, nil
	}
	if err != nil {
		return Attestation{}, false, err
	}
	if err := json.Unmarshal([]byte(options), &a.Options); err != nil {
		return Attestation{}, false, err
	}
	a.AnsweredAt, a.CreatedAt = time.UnixMilli(answered), time.UnixMilli(created)
	if spent != 0 {
		a.SpentAt = time.UnixMilli(spent)
	}
	return a, true, nil
}

// SpendAttestation marks nonce spent, and reports false when it already was.
func (s *Store) SpendAttestation(nonce string, at time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE relay_attestations SET spent_at = ? WHERE nonce = ? AND spent_at = 0`,
		at.UnixMilli(), nonce)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SealedMessage is the sender of message id when it was queued for recipient
// and claimed for typing, which every message a seal can stand on was. ok is
// false for any other id: one never sent to recipient, or never typed.
func (s *Store) SealedMessage(id int64, recipient string) (sender string, ok bool, err error) {
	var claimed int64
	err = s.db.QueryRow(`SELECT sender_id, claimed_at FROM session_inbox WHERE id = ? AND session_id = ?`,
		id, recipient).Scan(&sender, &claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return sender, claimed != 0, nil
}

// UnnotedAttestations are session's spent attestations whose note has not yet
// reached its auto-mode classifier, oldest first.
func (s *Store) UnnotedAttestations(session string) ([]Attestation, error) {
	rows, err := s.db.Query(`SELECT message_id FROM relay_attestations
		WHERE target_session = ? AND spent_at != 0 AND noted_at = 0 ORDER BY created_at`, session)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Attestation, 0, len(ids))
	for _, id := range ids {
		a, ok, err := s.AttestationFor(id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// MarkAttestationNoted records that nonce's note reached the classifier.
func (s *Store) MarkAttestationNoted(nonce string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE relay_attestations SET noted_at = ? WHERE nonce = ? AND noted_at = 0`,
		at.UnixMilli(), nonce)
	return err
}
