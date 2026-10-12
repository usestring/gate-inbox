package communications

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type ThreadRef struct {
	Source   string `json:"source"`
	Account  string `json:"account"`
	ThreadID string `json:"thread_id"`
}

type Message struct {
	ThreadRef
	ID        string    `json:"id"`
	Sender    string    `json:"sender"`
	ReplyTo   []string  `json:"reply_to,omitempty"`
	Direction string    `json:"direction"`
	Subject   string    `json:"subject,omitempty"`
	Body      string    `json:"body"`
	URL       string    `json:"url,omitempty"`
	SentAt    time.Time `json:"sent_at"`
}

type Conversation struct {
	ID string `json:"id"`
	ThreadRef
	Revision string    `json:"revision"`
	Unread   bool      `json:"unread"`
	Messages []Message `json:"messages"`
	Drafts   []Draft   `json:"drafts"`
}

type Summary struct {
	ID string `json:"id"`
	ThreadRef
	Revision  string    `json:"revision"`
	Unread    bool      `json:"unread"`
	Subject   string    `json:"subject,omitempty"`
	Sender    string    `json:"sender"`
	Preview   string    `json:"preview"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Draft struct {
	ID string `json:"id"`
	ThreadRef
	Revision   string    `json:"revision"`
	InReplyTo  string    `json:"in_reply_to"`
	Recipients []string  `json:"recipients"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
	Stale      bool      `json:"stale"`
}

func digest(values []string) string {
	if len(values) == 0 {
		values = []string{}
	}
	data, _ := json.Marshal(values)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (r ThreadRef) key() string { return digest([]string{r.Source, r.Account, r.ThreadID}) }

func (m Message) fingerprint() string {
	return digest([]string{m.ThreadRef.key(), m.ID, m.Sender, digest(m.ReplyTo), m.Direction, m.Subject, m.Body, m.URL, m.SentAt.UTC().Format(time.RFC3339Nano)})
}

func (r ThreadRef) validate() error {
	if !slices.Contains([]string{"slack", "email", "sms"}, r.Source) {
		return fmt.Errorf("source must be slack, email, or sms, got %q", r.Source)
	}
	for name, value := range map[string]string{"account": r.Account, "thread_id": r.ThreadID} {
		if strings.TrimSpace(value) == "" || len(value) > 512 {
			return fmt.Errorf("%s must contain 1-512 bytes", name)
		}
	}
	return nil
}

func (m Message) validate() error {
	if err := m.ThreadRef.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(m.ID) == "" || len(m.ID) > 512 {
		return errors.New("id must contain 1-512 bytes")
	}
	if strings.TrimSpace(m.Sender) == "" || len(m.Sender) > 512 {
		return errors.New("sender must contain 1-512 bytes")
	}
	if !slices.Contains([]string{"incoming", "outgoing"}, m.Direction) {
		return errors.New("direction must be incoming or outgoing")
	}
	if m.SentAt.IsZero() || m.SentAt.Year() < 0 || m.SentAt.Year() > 9999 {
		return errors.New("sent_at must be a nonzero RFC 3339 timestamp")
	}
	if len(m.Body) > 64<<10 || len(m.Subject) > 1024 || len(m.URL) > 4096 {
		return errors.New("message body, subject, or URL exceeds its size limit")
	}
	if m.Direction == "incoming" && len(m.ReplyTo) == 0 {
		return errors.New("incoming messages need explicit reply_to recipients")
	}
	if len(m.ReplyTo) > 100 {
		return errors.New("reply_to exceeds 100 recipients")
	}
	for _, to := range m.ReplyTo {
		if strings.TrimSpace(to) == "" || len(to) > 512 {
			return errors.New("reply_to recipients must contain 1-512 bytes")
		}
	}
	return nil
}
