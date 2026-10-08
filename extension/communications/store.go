package communications

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const maxStateBytes = 64 << 20
const maxRecords = 10000

type diskState struct {
	Version  int               `json:"version"`
	Messages []Message         `json:"messages"`
	Drafts   []Draft           `json:"drafts"`
	Seen     map[string]string `json:"seen"`
}

type Store struct{ dir string }

func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("communications data directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) transaction(ctx context.Context, change bool, run func(*diskState) error) error {
	unlock, err := lock(ctx, filepath.Join(s.dir, "inbox-v1.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	state := diskState{Version: 1, Seen: map[string]string{}}
	file, err := os.Open(filepath.Join(s.dir, "inbox-v1.json"))
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
		file.Close()
		if readErr != nil {
			return readErr
		}
		if len(data) > maxStateBytes {
			return errors.New("communications store exceeds 64 MiB")
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("read communications store: %w", err)
		}
		if state.Version != 1 {
			return fmt.Errorf("unsupported communications store version %d", state.Version)
		}
		if state.Seen == nil {
			state.Seen = map[string]string{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := run(&state); err != nil {
		return err
	}
	if !change {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > maxStateBytes {
		return errors.New("communications store exceeds 64 MiB")
	}
	temp, err := os.CreateTemp(s.dir, ".inbox-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), filepath.Join(s.dir, "inbox-v1.json"))
}

func (s *Store) Import(ctx context.Context, messages []Message) (int, error) {
	if len(messages) == 0 || len(messages) > 1000 {
		return 0, errors.New("import needs 1-1000 messages")
	}
	messages = slices.Clone(messages)
	for i := range messages {
		if err := messages[i].validate(); err != nil {
			return 0, fmt.Errorf("message %d: %w", i+1, err)
		}
		messages[i].SentAt = messages[i].SentAt.UTC()
	}
	added := 0
	err := s.transaction(ctx, true, func(state *diskState) error {
		existing := make(map[string]Message, len(state.Messages))
		for _, m := range state.Messages {
			existing[digest([]string{m.ThreadRef.key(), m.ID})] = m
		}
		for _, m := range messages {
			key := digest([]string{m.ThreadRef.key(), m.ID})
			if old, ok := existing[key]; ok {
				if old.fingerprint() != m.fingerprint() {
					return fmt.Errorf("message %q already exists with different content", m.ID)
				}
				continue
			}
			if len(state.Messages)+len(state.Drafts) >= maxRecords {
				return errors.New("communications store reached 10000 records")
			}
			state.Messages = append(state.Messages, m)
			existing[key] = m
			added++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

func conversations(state *diskState) []Conversation {
	grouped := map[string]*Conversation{}
	for _, m := range state.Messages {
		id := m.ThreadRef.key()
		if grouped[id] == nil {
			grouped[id] = &Conversation{ID: id, ThreadRef: m.ThreadRef, Messages: []Message{}, Drafts: []Draft{}}
		}
		grouped[id].Messages = append(grouped[id].Messages, m)
	}
	for _, d := range state.Drafts {
		if c := grouped[d.ThreadRef.key()]; c != nil {
			c.Drafts = append(c.Drafts, d)
		}
	}
	result := make([]Conversation, 0, len(grouped))
	for _, c := range grouped {
		slices.SortFunc(c.Messages, func(a, b Message) int {
			if order := a.SentAt.Compare(b.SentAt); order != 0 {
				return order
			}
			return cmp.Compare(a.ID, b.ID)
		})
		fingerprints := make([]string, 0, len(c.Messages))
		for _, m := range c.Messages {
			fingerprints = append(fingerprints, m.fingerprint())
		}
		c.Revision = digest(fingerprints)
		c.Unread = state.Seen[c.ID] != c.Revision
		for i := range c.Drafts {
			c.Drafts[i].Stale = c.Drafts[i].Revision != c.Revision
		}
		result = append(result, *c)
	}
	slices.SortFunc(result, func(a, b Conversation) int {
		if order := b.Messages[len(b.Messages)-1].SentAt.Compare(a.Messages[len(a.Messages)-1].SentAt); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return result
}

func find(state *diskState, id string) (Conversation, error) {
	for _, c := range conversations(state) {
		if c.ID == id {
			return c, nil
		}
	}
	return Conversation{}, fmt.Errorf("conversation %q not found", id)
}

func (s *Store) List(ctx context.Context) ([]Summary, error) {
	result := []Summary{}
	err := s.transaction(ctx, false, func(state *diskState) error {
		for _, c := range conversations(state) {
			last := c.Messages[len(c.Messages)-1]
			preview := []rune(last.Body)
			if len(preview) > 200 {
				preview = preview[:200]
			}
			result = append(result, Summary{ID: c.ID, ThreadRef: c.ThreadRef, Revision: c.Revision, Unread: c.Unread, Subject: last.Subject, Sender: last.Sender, Preview: string(preview), UpdatedAt: last.SentAt})
		}
		return nil
	})
	return result, err
}

func (s *Store) Read(ctx context.Context, id string) (Conversation, error) {
	var result Conversation
	err := s.transaction(ctx, false, func(state *diskState) error {
		var err error
		result, err = find(state, id)
		return err
	})
	return result, err
}

func (s *Store) MarkRead(ctx context.Context, id, revision string) error {
	return s.transaction(ctx, true, func(state *diskState) error {
		c, err := find(state, id)
		if err != nil {
			return err
		}
		if revision != c.Revision {
			return errors.New("conversation changed; read it again before marking it read")
		}
		state.Seen[id] = revision
		return nil
	})
}

func (s *Store) Draft(ctx context.Context, id, revision, body string) (Draft, error) {
	if strings.TrimSpace(body) == "" || len(body) > 64<<10 {
		return Draft{}, errors.New("draft body must contain 1-65536 bytes")
	}
	var result Draft
	err := s.transaction(ctx, true, func(state *diskState) error {
		c, err := find(state, id)
		if err != nil {
			return err
		}
		if revision != c.Revision {
			return errors.New("conversation changed; read it again before drafting a reply")
		}
		if len(state.Messages)+len(state.Drafts) >= maxRecords {
			return errors.New("communications store reached 10000 records")
		}
		for _, m := range slices.Backward(c.Messages) {
			if m.Direction != "incoming" {
				continue
			}
			var bytes [16]byte
			if _, err := rand.Read(bytes[:]); err != nil {
				return err
			}
			result = Draft{ID: hex.EncodeToString(bytes[:]), ThreadRef: c.ThreadRef, Revision: revision, InReplyTo: m.ID, Recipients: slices.Clone(m.ReplyTo), Body: body, CreatedAt: time.Now().UTC()}
			state.Drafts = append(state.Drafts, result)
			return nil
		}
		return errors.New("conversation has no incoming message to reply to")
	})
	if err != nil {
		return Draft{}, err
	}
	return result, nil
}
