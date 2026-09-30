package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/status"
)

// A message queued for a session that then ends is never typed in: the pane
// it was waiting for is gone, and a revive is a choice nobody has made yet.
// Left in the queue it reads as pending for as long as anyone looks, so a
// sender that queued a redirect to a worker that then died believes the
// redirect is on its way. Nothing at send time can report a death that has
// not happened, so the queue is resolved at the death instead, and each
// sender is told once which of its messages went with the recipient.

// DropRecipientEnded is the drop reason for a message whose recipient ended
// before it was typed in.
const DropRecipientEnded = "recipient ended"

// ResolveEndedRecipient drops every message still queued for sessionID and
// tells each session that sent one, once, which of its messages were
// dropped. It reports the messages it dropped.
//
// It acts only while the row reads dead. A restart or an account switch ends
// the pane and relaunches it straight away, and a pass that saw the pane
// missing between the two must not drop what the relaunched agent is about to
// read.
//
// A claimed message is dropped too: its paste was on its way to a pane that
// no longer exists.
func (s *Store) ResolveEndedRecipient(sessionID string, at time.Time) ([]InboxMessage, error) {
	stamp := encodeTime(at)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var recipientName string
	err = tx.QueryRow(`SELECT name FROM sessions WHERE id = ? AND status = ?`, sessionID, status.Dead).Scan(&recipientName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(`
SELECT id, sender_id, sender_name, body, sent_at
  FROM session_inbox
 WHERE session_id = ? AND delivered_at = 0 AND dropped_at = 0
 ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	var dropped []InboxMessage
	for rows.Next() {
		msg := InboxMessage{SessionID: sessionID, DroppedAt: at, DropReason: DropRecipientEnded}
		var sentAt int64
		if err := rows.Scan(&msg.ID, &msg.SenderID, &msg.SenderName, &msg.Body, &sentAt); err != nil {
			rows.Close()
			return nil, err
		}
		msg.SentAt = decodeTime(sentAt)
		dropped = append(dropped, msg)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	if _, err := tx.Exec(`
UPDATE session_inbox SET delivered_at = ?, dropped_at = ?, drop_reason = ?
 WHERE session_id = ? AND delivered_at = 0 AND dropped_at = 0`,
		stamp, stamp, DropRecipientEnded, sessionID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return dropped, s.tellSendersRecipientEnded(sessionID, recipientName, dropped, at)
}

// tellSendersRecipientEnded queues one notice per sending session. The
// operator and extensions have no prompt to read one in, and a sender that
// has been archived is not reading either.
func (s *Store) tellSendersRecipientEnded(recipientID, recipientName string, dropped []InboxMessage, at time.Time) error {
	ids := map[string][]int64{}
	var senders []string
	for _, msg := range dropped {
		if SpeaksAsOperator(msg.SenderID) {
			continue
		}
		if _, ok := ExtensionSender(msg.SenderID); ok {
			continue
		}
		if FromSystem(msg.SenderID) {
			continue
		}
		if _, seen := ids[msg.SenderID]; !seen {
			senders = append(senders, msg.SenderID)
		}
		ids[msg.SenderID] = append(ids[msg.SenderID], msg.ID)
	}
	sort.Strings(senders)
	for _, senderID := range senders {
		sender, err := s.Get(senderID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if sender.Archived {
			continue
		}
		body := recipientEndedMessage(recipientID, recipientName, ids[senderID])
		// Straight in, past the queue, rate and dedupe guards: those stop two
		// agents talking in a loop, and this is one notice per death whose
		// ids no other notice carries. Refused, it would be the only news
		// the sender gets, lost.
		if _, err := s.db.Exec(`
INSERT INTO session_inbox (session_id, sender_id, sender_name, body, fingerprint, sent_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			sender.ID, recipientID, recipientName, body, textfmt.Fingerprint(body), encodeTime(at)); err != nil {
			return fmt.Errorf("session %s was not told its messages to %s were dropped: %w", sender.ID, recipientID, err)
		}
	}
	return nil
}

func recipientEndedMessage(recipientID, recipientName string, ids []int64) string {
	list := make([]string, len(ids))
	for i, id := range ids {
		list[i] = strconv.FormatInt(id, 10)
	}
	noun, pronoun := "message", "it"
	if len(ids) > 1 {
		noun, pronoun = "messages", "them"
	}
	return fmt.Sprintf("%s (session %s) ended before your %s %s reached its prompt, so %s will never be typed in "+
		"and now read as dropped: %s. If you still need %s delivered, revive the session and send again.",
		recipientName, recipientID, noun, strings.Join(list, ", "), pronoun, DropRecipientEnded, pronoun)
}
