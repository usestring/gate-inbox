// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/usestring/gate-inbox/internal/tracing"
)

// HumanSenderID marks a message the operator typed at a shell, so it is
// delivered as the operator's own words rather than fenced as another
// agent's.
//
// The fence is what tells a worker that text arriving at its prompt is not
// its user speaking, and every queued message got one -- including a person's
// own `send` from a terminal, which arrives under whatever session id their
// shell happens to carry. Workers refused those as injection attempts. This is
// how the queue carries the distinction the TUI's own typing path never had
// to make.
//
// It is not a session id and cannot collide with one: ids are hex.
const HumanSenderID = "human"

// InboxMessage is one agent-to-agent message waiting to be typed into a
// session's prompt. It rides its own table rather than PendingInputs
// because a launch prompt and a message need different delivery gates,
// and because a per-row claim survives a concurrent append from the MCP
// process, which the pending-input blob compare-and-set does not.
type InboxMessage struct {
	ID          int64
	SessionID   string
	SenderID    string
	SenderName  string
	Body        string
	Fingerprint string
	// Subject names what a message is about, so a later one on the same
	// subject replaces the earlier one still waiting instead of stacking
	// behind it. Empty means the message stands on its own, which is every
	// message the caller did not label.
	Subject      string
	SentAt       time.Time
	ClaimedAt    time.Time
	DeliveredAt  time.Time
	DroppedAt    time.Time
	ReadAt       time.Time
	SupersededBy int64
	// Interrupt asks for the recipient's running turn to be stopped before
	// the message is typed, so it is read now rather than after the step in
	// hand. Never at the cost of a dialog: one on screen holds it like any
	// other message.
	Interrupt bool
}

// InboxLimits stop two agents talking each other into an infinite loop.
type InboxLimits struct {
	QueueCap     int
	RateCap      int
	RateWindow   time.Duration
	DedupeWindow time.Duration
}

var DefaultInboxLimits = InboxLimits{
	QueueCap:     20,
	RateCap:      5,
	RateWindow:   time.Minute,
	DedupeWindow: 10 * time.Minute,
}

// PollerHeartbeatKey is stamped by the manager while it polls. A session
// tool reads it to tell a sender whether a manager is running to deliver
// what it queues.
const PollerHeartbeatKey = "poller_heartbeat"

const (
	// PollerHeartbeatPeriod is how often the manager restamps that row.
	PollerHeartbeatPeriod = 10 * time.Second
	// PollerHeartbeatStale is when a stamp stops meaning a manager is home:
	// one period, plus room for a poll that ran long.
	PollerHeartbeatStale = 30 * time.Second
)

var (
	ErrInboxFull        = errors.New("the recipient's queue is full; wait for it to read what is already queued")
	ErrInboxRateLimited = errors.New("too many messages to this session in the last minute")
	ErrInboxDuplicate   = errors.New("an identical message is already queued or was just sent")
)

// Enqueue appends one message, retiring whatever the sender still has
// waiting on the same subject, and reports how many that was. Every limit
// rides the INSERT itself, so a second process cannot slip past a check that
// ran as its own statement.
func (s *Store) Enqueue(msg InboxMessage, limits InboxLimits) (int64, int, error) {
	if !tracing.Enabled() {
		return s.enqueue(msg, limits)
	}
	start := time.Now()
	id, superseded, err := s.enqueue(msg, limits)
	recordOp("store.Enqueue", start, err, true, tracing.Attr{Key: "superseded", Value: superseded})
	return id, superseded, err
}

// enqueue runs the insert and, for a subject, the supersession around it in
// one transaction. The order is retire-then-insert because the queue cap
// counts undelivered rows: a correction to a recipient already holding
// twenty would otherwise be refused by the very messages it replaces. A
// refused insert rolls the retirement back with it, so a sender never loses
// the instruction it already had queued to a send that did not land.
func (s *Store) enqueue(msg InboxMessage, limits InboxLimits) (int64, int, error) {
	if msg.Subject == "" {
		id, err := s.insertMessage(s.db, msg, limits)
		return id, 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	// Claimed messages are left alone: a claim means a paste is on its way
	// to the pane, so retiring one would tell its sender it was replaced by
	// a message the recipient is about to read anyway.
	res, err := tx.Exec(`
UPDATE session_inbox SET delivered_at = ?, superseded_by = -1
 WHERE session_id = ? AND sender_id = ? AND subject = ?
   AND delivered_at = 0 AND claimed_at = 0`,
		encodeTime(msg.SentAt), msg.SessionID, msg.SenderID, msg.Subject)
	if err != nil {
		return 0, 0, err
	}
	superseded, err := res.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	id, err := s.insertMessage(tx, msg, limits)
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(`
UPDATE session_inbox SET superseded_by = ?
 WHERE session_id = ? AND sender_id = ? AND subject = ? AND superseded_by = -1`,
		id, msg.SessionID, msg.SenderID, msg.Subject); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return id, int(superseded), nil
}

// execer is the db handle an insert runs on, so the same statement serves a
// bare append and the transaction a supersession needs around it.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func (s *Store) insertMessage(db execer, msg InboxMessage, limits InboxLimits) (int64, error) {
	sentAt := encodeTime(msg.SentAt)
	rateFrom := encodeTime(msg.SentAt.Add(-limits.RateWindow))
	dedupeFrom := encodeTime(msg.SentAt.Add(-limits.DedupeWindow))
	res, err := db.Exec(`
INSERT INTO session_inbox (session_id, sender_id, sender_name, body, fingerprint, subject, interrupt, sent_at)
SELECT ?, ?, ?, ?, ?, ?, ?, ?
WHERE (SELECT COUNT(*) FROM session_inbox WHERE session_id = ? AND delivered_at = 0) < ?
  AND (SELECT COUNT(*) FROM session_inbox WHERE session_id = ? AND sender_id = ? AND sent_at >= ?) < ?
  AND NOT EXISTS (
    SELECT 1 FROM session_inbox
     WHERE session_id = ? AND sender_id = ? AND fingerprint = ? AND sent_at >= ?
  )`,
		msg.SessionID, msg.SenderID, msg.SenderName, msg.Body, msg.Fingerprint, msg.Subject, msg.Interrupt, sentAt,
		msg.SessionID, limits.QueueCap,
		msg.SessionID, msg.SenderID, rateFrom, limits.RateCap,
		msg.SessionID, msg.SenderID, msg.Fingerprint, dedupeFrom)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected == 0 {
		return 0, s.rejectedEnqueue(db, msg, limits)
	}
	return res.LastInsertId()
}

// rejectedEnqueue names which guard turned the message away, so the
// sender learns whether to wait, slow down, or stop repeating itself.
func (s *Store) rejectedEnqueue(db execer, msg InboxMessage, limits InboxLimits) error {
	var queued, recent, duplicate int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM session_inbox WHERE session_id = ? AND delivered_at = 0`,
		msg.SessionID).Scan(&queued); err != nil {
		return err
	}
	if queued >= limits.QueueCap {
		return ErrInboxFull
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM session_inbox WHERE session_id = ? AND sender_id = ? AND sent_at >= ?`,
		msg.SessionID, msg.SenderID, encodeTime(msg.SentAt.Add(-limits.RateWindow))).Scan(&recent); err != nil {
		return err
	}
	if recent >= limits.RateCap {
		return ErrInboxRateLimited
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM session_inbox WHERE session_id = ? AND sender_id = ? AND fingerprint = ? AND sent_at >= ?`,
		msg.SessionID, msg.SenderID, msg.Fingerprint, encodeTime(msg.SentAt.Add(-limits.DedupeWindow))).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate > 0 {
		return ErrInboxDuplicate
	}
	return errors.New("message was not queued")
}

// HeadMessages returns the oldest undelivered message for every session that
// has one, keyed by session id.
//
// One query rather than one per session. The poll pass asked HeadMessage for
// each row on the board every two seconds, and the store holds a single
// connection, so those thirty round trips did not merely cost their own time:
// they occupied the connection a keypress needed to write through, which is
// how pressing a key came to wait on the pass.
//
// The subquery is spelled out rather than leaning on SQLite's bare-column
// rule for a lone MIN(), which is real but obscure enough that the next
// reader would have to look it up to know this is not a bug.
func (s *Store) HeadMessages() (map[string]InboxMessage, error) {
	if !tracing.Enabled() {
		return s.headMessages()
	}
	start := time.Now()
	heads, err := s.headMessages()
	recordOp("store.HeadMessages", start, err, false, tracing.Attr{Key: "rows", Value: len(heads)})
	return heads, err
}

func (s *Store) headMessages() (map[string]InboxMessage, error) {
	rows, err := s.db.Query(`
SELECT session_id, id, sender_id, sender_name, body, fingerprint, interrupt, sent_at, claimed_at
  FROM session_inbox AS outer_msg
 WHERE delivered_at = 0
   AND id = (SELECT MIN(id) FROM session_inbox AS inner_msg
              WHERE inner_msg.session_id = outer_msg.session_id
                AND inner_msg.delivered_at = 0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	heads := map[string]InboxMessage{}
	for rows.Next() {
		var msg InboxMessage
		var sentAt, claimedAt int64
		if err := rows.Scan(&msg.SessionID, &msg.ID, &msg.SenderID, &msg.SenderName,
			&msg.Body, &msg.Fingerprint, &msg.Interrupt, &sentAt, &claimedAt); err != nil {
			return nil, err
		}
		msg.SentAt = decodeTime(sentAt)
		msg.ClaimedAt = decodeTime(claimedAt)
		heads[msg.SessionID] = msg
	}
	return heads, rows.Err()
}

// HeadMessage returns the oldest message still waiting for delivery.
func (s *Store) HeadMessage(sessionID string) (InboxMessage, bool, error) {
	msg := InboxMessage{SessionID: sessionID}
	var sentAt, claimedAt int64
	err := s.db.QueryRow(`
SELECT id, sender_id, sender_name, body, fingerprint, interrupt, sent_at, claimed_at
  FROM session_inbox
 WHERE session_id = ? AND delivered_at = 0
 ORDER BY id LIMIT 1`, sessionID).
		Scan(&msg.ID, &msg.SenderID, &msg.SenderName, &msg.Body, &msg.Fingerprint, &msg.Interrupt, &sentAt, &claimedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return InboxMessage{}, false, nil
	}
	if err != nil {
		return InboxMessage{}, false, err
	}
	msg.SentAt = decodeTime(sentAt)
	msg.ClaimedAt = decodeTime(claimedAt)
	return msg, true, nil
}

// ClaimMessage takes ownership of one message before it is typed. A
// concurrent append cannot invalidate this compare-and-set the way it can
// invalidate the pending-input one, because the guard is a single row.
func (s *Store) ClaimMessage(id int64, at time.Time) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE session_inbox SET claimed_at = ? WHERE id = ? AND claimed_at = 0 AND delivered_at = 0`,
		encodeTime(at), id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected == 1, err
}

func (s *Store) MarkDelivered(id int64, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE session_inbox SET delivered_at = ? WHERE id = ? AND delivered_at = 0`,
		encodeTime(at), id)
	return err
}

// MarkDropped retires a message that never reached the pane. It leaves the
// queue exactly as a delivered one does, since delivery is at most once and
// nothing may retype it, and dropped_at is what tells its sender the
// difference.
// ForwardInbox re-addresses a retired session's undelivered messages to the
// session that replaced it, and reports how many moved.
//
// A restart destroys a conversation, not the goal, and a message queued for
// the old worker was sent to whoever holds that goal now. Left where it was,
// it would sit against a dead row forever -- a child's report to a parent that
// was restarted mid-run is the case this was written for.
//
// A claimed-but-undelivered message moves too, and its claim is cleared. The
// claim exists so one instruction is not typed twice into one conversation;
// the conversation it might have reached is being thrown away, so the risk it
// guards against cannot happen, and the alternative is losing a report nobody
// can recover.
func (s *Store) ForwardInbox(fromSessionID, toSessionID string) (int64, error) {
	if fromSessionID == "" || toSessionID == "" || fromSessionID == toSessionID {
		return 0, nil
	}
	res, err := s.db.Exec(`
UPDATE session_inbox SET session_id = ?, claimed_at = 0
 WHERE session_id = ? AND delivered_at = 0 AND dropped_at = 0`,
		toSessionID, fromSessionID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) MarkDropped(id int64, at time.Time) error {
	stamp := encodeTime(at)
	_, err := s.db.Exec(
		`UPDATE session_inbox SET delivered_at = ?, dropped_at = ? WHERE id = ? AND delivered_at = 0`,
		stamp, stamp, id)
	return err
}

// MarkRead acks every message a session received from one sender. A reply
// is the ack: the recipient answering proves it read them. A dropped
// message was never in front of it to read.
func (s *Store) MarkRead(sessionID, senderID string, at time.Time) error {
	_, err := s.db.Exec(`
UPDATE session_inbox SET read_at = ?
 WHERE session_id = ? AND sender_id = ? AND delivered_at != 0 AND dropped_at = 0 AND read_at = 0`,
		encodeTime(at), sessionID, senderID)
	return err
}

// Message reads back one message for the session that sent it or the one
// that spawned its recipient, so either can confirm delivery without reading
// the recipient's screen. A coordinator owns what reaches its children, and
// otherwise could not tell whether another session's correction to one of
// them ever landed.
func (s *Store) Message(id int64, callerID string) (InboxMessage, error) {
	msg := InboxMessage{ID: id}
	var sentAt, claimedAt, deliveredAt, droppedAt, readAt int64
	err := s.db.QueryRow(`
SELECT i.session_id, i.sender_id, i.sender_name, i.body, i.fingerprint, i.subject, i.superseded_by, i.interrupt,
       i.sent_at, i.claimed_at, i.delivered_at, i.dropped_at, i.read_at
  FROM session_inbox i LEFT JOIN sessions s ON s.id = i.session_id
 WHERE i.id = ? AND (i.sender_id = ? OR `+spawnerColumnOf("s")+` = ?)`, id, callerID, callerID).
		Scan(&msg.SessionID, &msg.SenderID, &msg.SenderName, &msg.Body, &msg.Fingerprint, &msg.Subject, &msg.SupersededBy, &msg.Interrupt,
			&sentAt, &claimedAt, &deliveredAt, &droppedAt, &readAt)
	if err != nil {
		return InboxMessage{}, err
	}
	msg.SentAt = decodeTime(sentAt)
	msg.ClaimedAt = decodeTime(claimedAt)
	msg.DeliveredAt = decodeTime(deliveredAt)
	msg.DroppedAt = decodeTime(droppedAt)
	msg.ReadAt = decodeTime(readAt)
	return msg, nil
}

// QueuedCount is how many messages a session has waiting, which the sender
// reports as a queue position and the list view shows as a badge.
func (s *Store) QueuedCount(sessionID string) (int, error) {
	var queued int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM session_inbox WHERE session_id = ? AND delivered_at = 0`,
		sessionID).Scan(&queued)
	return queued, err
}

// QueuedCounts is every session's waiting count in one query, for the
// poller's per-tick refresh.
func (s *Store) QueuedCounts() (map[string]int, error) {
	if !tracing.Enabled() {
		return s.queuedCounts()
	}
	start := time.Now()
	counts, err := s.queuedCounts()
	recordOp("store.QueuedCounts", start, err, false, tracing.Attr{Key: "rows", Value: len(counts)})
	return counts, err
}

func (s *Store) queuedCounts() (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT session_id, COUNT(*) FROM session_inbox WHERE delivered_at = 0 GROUP BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		counts[id] = count
	}
	return counts, rows.Err()
}

// PruneInbox drops delivered messages past their retention window; the
// undelivered ones are the queue and are never swept.
func (s *Store) PruneInbox(before time.Time) error {
	if !tracing.Enabled() {
		_, err := s.pruneInbox(before)
		return err
	}
	start := time.Now()
	// How many rows a sweep deleted is what makes this span readable. It
	// runs on one pass in three hundred and that pass waits for it, so the
	// sweep that finally found a month of delivered messages has to be
	// tellable from the ones that found none, rather than arriving as a
	// single pass that was inexplicably slow.
	deleted, err := s.pruneInbox(before)
	recordOp("store.PruneInbox", start, err, true, tracing.Attr{Key: "rows", Value: deleted})
	return err
}

func (s *Store) pruneInbox(before time.Time) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM session_inbox WHERE delivered_at != 0 AND delivered_at < ?`,
		encodeTime(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ReportsTo is what other sessions have sent this one, newest first.
//
// A child that finishes reports back to its parent through the inbox, so this
// is the only record of what a fan-out actually produced -- the pane that
// said it has usually been archived by the time anybody looks.
//
// Messages from a person are not reports and are left out: sender_id is empty
// for those, and a parent's screen is asking what its children did.
func (s *Store) ReportsTo(sessionID string, limit int) ([]InboxMessage, error) {
	if sessionID == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`
SELECT id, sender_id, sender_name, body, sent_at, delivered_at
  FROM session_inbox
 WHERE session_id = ? AND sender_id != ''
 ORDER BY sent_at DESC, id DESC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxMessage
	for rows.Next() {
		msg := InboxMessage{SessionID: sessionID}
		var sentAt, deliveredAt int64
		if err := rows.Scan(&msg.ID, &msg.SenderID, &msg.SenderName, &msg.Body,
			&sentAt, &deliveredAt); err != nil {
			return nil, err
		}
		msg.SentAt, msg.DeliveredAt = decodeTime(sentAt), decodeTime(deliveredAt)
		out = append(out, msg)
	}
	return out, rows.Err()
}

// ChildRestSubject labels the board's own notice that a child has come to
// rest, so a child that stops, is restarted and stops again leaves its
// parent one current notice rather than a queue of stale ones.
func ChildRestSubject(childID string) string { return "child-rest:" + childID }
