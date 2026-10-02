// Package gatechannel prepares revisioned snapshots of pending questions.
// Revisions let desktop, web, mobile, or other delivery clients detect changed
// content after reconnects or handoffs and bind an answer to what was presented.
// A matching revision is not proof that a call is still pending; accepting an
// answer also requires validating the current call identity and pending state.
package gatechannel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/usestring/gate-inbox/internal/convo"
	"slices"
	"strings"
	"time"
)

type Envelope struct {
	Version        int                 `json:"version"`
	ID             string              `json:"id"`
	Epoch          string              `json:"epoch"`
	SessionID      string              `json:"session_id"`
	AgentSessionID string              `json:"agent_session_id"`
	ToolUseID      string              `json:"tool_use_id"`
	Revision       string              `json:"revision"`
	AskedAt        time.Time           `json:"asked_at"`
	Questions      []convo.AskQuestion `json:"questions"`
}

func NewEnvelope(epoch, sessionID, agentSessionID string, call convo.AskCall) (Envelope, error) {
	if strings.TrimSpace(epoch) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(agentSessionID) == "" || strings.TrimSpace(call.ToolUseID) == "" {
		return Envelope{}, errors.New("gate identity requires epoch, session, agent session and tool call")
	}
	if len(call.Questions) == 0 {
		return Envelope{}, errors.New("gate requires at least one question")
	}
	questions := slices.Clone(call.Questions)
	for i := range questions {
		if strings.TrimSpace(questions[i].Question) == "" {
			return Envelope{}, errors.New("gate question text is empty")
		}
		questions[i].Options = slices.Clone(questions[i].Options)
		if questions[i].Options == nil {
			questions[i].Options = []convo.AskOption{}
		}
	}
	raw, err := json.Marshal(questions)
	if err != nil {
		return Envelope{}, err
	}
	revision := digest(raw)
	identity := struct {
		Version        int    `json:"version"`
		Epoch          string `json:"epoch"`
		SessionID      string `json:"session_id"`
		AgentSessionID string `json:"agent_session_id"`
		ToolUseID      string `json:"tool_use_id"`
		Revision       string `json:"revision"`
	}{1, epoch, sessionID, agentSessionID, call.ToolUseID, revision}
	raw, err = json.Marshal(identity)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Version: 1, ID: digest(raw), Epoch: epoch, SessionID: sessionID, AgentSessionID: agentSessionID, ToolUseID: call.ToolUseID, Revision: revision, AskedAt: call.AskedAt, Questions: questions}, nil
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
