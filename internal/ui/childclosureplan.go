package ui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

const childClosurePlanFile = "child-closure-plans.jsonl"

type childClosurePlan struct {
	PlannedAt      time.Time `json:"planned_at"`
	SessionID      string    `json:"session_id"`
	SpawnerID      string    `json:"spawner_id"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Tool           string    `json:"tool"`
	Status         string    `json:"status"`
	StatusAt       time.Time `json:"status_at"`
	Kind           string    `json:"kind"`
	Reason         string    `json:"reason"`
	RelatedIDs     []string  `json:"related_ids,omitempty"`
}

type childClosureKey struct {
	sessionID  string
	statusAt   time.Time
	kind       string
	reason     string
	relatedIDs string
}

func newChildClosurePlan(sess store.Session, kind, reason string, relatedIDs []string) childClosurePlan {
	conversationID := sess.AgentSessionID
	if conversationID == "" {
		conversationID = sess.RetiredAgentSessionID
	}
	slices.Sort(relatedIDs)
	return childClosurePlan{
		PlannedAt: time.Now().UTC(), SessionID: sess.ID, SpawnerID: store.SpawnerOf(sess),
		ConversationID: conversationID, Tool: sess.Tool, Status: sess.Status,
		StatusAt: sess.LastStatusAt.UTC(), Kind: kind, Reason: reason, RelatedIDs: relatedIDs,
	}
}

func (m *Model) recordChildClosurePlans(plans []childClosurePlan) error {
	active := make(map[childClosureKey]bool, len(plans))
	var fresh []childClosurePlan
	for _, plan := range plans {
		key := childClosureKey{
			sessionID: plan.SessionID, statusAt: plan.StatusAt, kind: plan.Kind,
			reason: plan.Reason, relatedIDs: strings.Join(plan.RelatedIDs, ","),
		}
		if !active[key] && !m.childClosurePlans[key] {
			fresh = append(fresh, plan)
		}
		active[key] = true
	}
	if len(fresh) > 0 {
		dir := m.configDir()
		if dir == "" {
			return errors.New("configuration directory is unavailable")
		}
		if err := appendChildClosurePlans(filepath.Join(dir, childClosurePlanFile), fresh); err != nil {
			return err
		}
	}
	m.childClosurePlans = active
	return nil
}

func appendChildClosurePlans(path string, plans []childClosurePlan) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, plan := range plans {
		if err := enc.Encode(plan); err != nil {
			return errors.Join(err, f.Close())
		}
	}
	return f.Close()
}
