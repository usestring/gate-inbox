package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"
)

var feedbackEventKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type PRDelivery struct {
	Status    string `json:"status"`
	URL       string `json:"url"`
	EventKey  string `json:"event_key"`
	MessageID int64  `json:"message_id"`
	SessionID string `json:"session_id"`
}

type prReceipt struct {
	PRDelivery
	Payload string `json:"payload"`
}

// EnqueuePRFeedback commits acceptance and its receipt together. The callback
// may check panes, but must not use this Store while its transaction is open.
// A receipt is acceptance, not proof of delivery; retries never requeue it.
func (s *Store) EnqueuePRFeedback(url, eventKey, expectedID, body string, tools []string, live func(Session) (bool, error), at time.Time) (PRDelivery, error) {
	url, err := CanonicalPRURL(url)
	if err != nil {
		return PRDelivery{}, err
	}
	if !utf8.ValidString(body) || !feedbackEventKey.MatchString(eventKey) || expectedID == "" || len(expectedID) > 128 || len(body) == 0 || len(body) > 8000 {
		return PRDelivery{}, errors.New("invalid PR feedback key, recipient, URL or message size")
	}
	if live == nil {
		return PRDelivery{}, errors.New("PR feedback needs a live-recipient check")
	}
	sum := sha256.Sum256([]byte(url + "\x00" + body))
	payload := hex.EncodeToString(sum[:])
	key := "pr-delivery:v1:" + eventKey
	tx, err := s.db.Begin()
	if err != nil {
		return PRDelivery{}, err
	}
	defer tx.Rollback()
	var raw string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PRDelivery{}, err
	}
	if raw != "" {
		var receipt prReceipt
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
			return PRDelivery{}, fmt.Errorf("read PR feedback receipt: %w", err)
		}
		if receipt.URL != url || receipt.EventKey != eventKey || receipt.MessageID <= 0 || receipt.SessionID == "" {
			return PRDelivery{}, errors.New("invalid stored PR feedback receipt")
		}
		if receipt.Payload != payload {
			return PRDelivery{}, errors.New("PR feedback event key already accepted with a different payload")
		}
		var current string
		err := tx.QueryRow(`SELECT session_id FROM session_inbox WHERE id = ?`, receipt.MessageID).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return PRDelivery{}, err
		}
		if current != "" {
			receipt.SessionID = current
		}
		receipt.Status = "duplicate"
		return receipt.PRDelivery, nil
	}
	owners, err := prOwners(tx, url, tools)
	if err != nil {
		return PRDelivery{}, err
	}
	var active []Session
	for _, owner := range owners {
		running, err := live(owner)
		if err != nil {
			return PRDelivery{}, err
		}
		if running {
			active = append(active, owner)
		}
	}
	if len(active) != 1 || active[0].ID != expectedID {
		return PRDelivery{}, errors.New("PR feedback owner changed, is unavailable or is ambiguous")
	}
	id, err := s.insertMessage(tx, InboxMessage{
		SessionID: expectedID, SenderID: SystemSenderID, SenderName: "Gate Inbox PR feedback",
		Subject: key, Body: body, Fingerprint: key, SentAt: at,
	}, DefaultInboxLimits)
	if err != nil {
		return PRDelivery{}, err
	}
	receipt := prReceipt{PRDelivery: PRDelivery{Status: "accepted", URL: url, EventKey: eventKey, MessageID: id, SessionID: expectedID}, Payload: payload}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return PRDelivery{}, err
	}
	if _, err := tx.Exec(`INSERT INTO settings (key,value) VALUES (?,?)`, key, string(encoded)); err != nil {
		return PRDelivery{}, err
	}
	return receipt.PRDelivery, tx.Commit()
}
