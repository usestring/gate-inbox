package ui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

const jevFinishPositivesFile = "finish-check-positives.jsonl"

type jevFinishPositive struct {
	RecordedAt     time.Time        `json:"recorded_at"`
	SessionID      string           `json:"session_id"`
	ConversationID string           `json:"conversation_id,omitempty"`
	FinishAt       time.Time        `json:"finish_at"`
	EvidenceID     string           `json:"evidence_id"`
	Classifier     string           `json:"classifier"`
	Input          jevFinishInput   `json:"input"`
	Scores         jevFinishVerdict `json:"scores"`
}

func (m *Model) recordJevFinishPositive(sess store.Session, provider string, msg jevFinishResultMsg) error {
	dir := m.configDir()
	if dir == "" {
		return errors.New("configuration directory is unavailable")
	}
	row := jevFinishPositive{
		RecordedAt: time.Now().UTC(), SessionID: sess.ID, ConversationID: sess.AgentSessionID,
		FinishAt: sess.LastStatusAt.UTC(), EvidenceID: msg.digest, Classifier: provider,
		Input: msg.input, Scores: msg.verdict,
	}
	f, err := os.OpenFile(filepath.Join(dir, jevFinishPositivesFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return errors.Join(json.NewEncoder(f).Encode(row), f.Close())
}
