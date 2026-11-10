package store

// LastDelivered is the most recent message typed into each session, keyed by
// session. Triage reads it to tell a finish the operator's work produced from
// one a board chore produced: a prompt-cache keep-alive starts a turn that
// ends in a fresh finish with nothing new in it.
//
// One query for the whole board, like HeadMessages, because the poll pass
// asks for it every pass.
func (s *Store) LastDelivered() (map[string]InboxMessage, error) {
	rows, err := s.db.Query(`
SELECT session_id, id, sender_id, sender_name, body, subject, delivered_at
  FROM session_inbox AS outer_msg
 WHERE delivered_at > 0
   AND id = (SELECT MAX(id) FROM session_inbox AS inner_msg
              WHERE inner_msg.session_id = outer_msg.session_id
                AND inner_msg.delivered_at > 0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	last := map[string]InboxMessage{}
	for rows.Next() {
		var msg InboxMessage
		var deliveredAt int64
		if err := rows.Scan(&msg.SessionID, &msg.ID, &msg.SenderID, &msg.SenderName,
			&msg.Body, &msg.Subject, &deliveredAt); err != nil {
			return nil, err
		}
		msg.DeliveredAt = decodeTime(deliveredAt)
		last[msg.SessionID] = msg
	}
	return last, rows.Err()
}
