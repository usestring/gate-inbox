package store

import (
	"database/sql"
	"errors"
)

// Quiet delivery.
//
// A message one agent sends another used to be typed into the recipient's
// pane whole: header, fences, body and seal. In the pane it reads as the
// recipient's user typing, and on a busy board it was most of what a person
// scrolling a session saw (70% of turns in the median long session). For a
// recipient whose own UserPromptSubmit hook can carry text, the pane now gets
// one sealed pointer line instead, and the hook hands the model the full text
// kept here as additionalContext, which Claude Code shows the model and keeps
// out of the conversation a person scrolls.

// SetQuietText keeps the full text of message id, rendered for the session
// it is about to be typed into, for that session's hook to hand its model.
func (s *Store) SetQuietText(id int64, text string) error {
	_, err := s.db.Exec(`UPDATE session_inbox SET quiet_text = ? WHERE id = ?`, text, id)
	return err
}

// QuietText is the full text kept for message id, delivered to recipient.
// ok is false when no such message was queued for that recipient or it was
// typed in whole.
func (s *Store) QuietText(id int64, recipient string) (text string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT quiet_text FROM session_inbox WHERE id = ? AND session_id = ?`,
		id, recipient).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return text, text != "", nil
}
