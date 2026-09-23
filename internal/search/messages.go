package search

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/usestring/gate-inbox/internal/opencode"
)

type Message struct {
	Role string
	Text string
}

// ReadMessages reads a bounded tail without admitting tool or harness traffic.
func ReadMessages(target Target, database string) ([]Message, error) {
	if target.Tool == ToolOpenCode {
		return readOpenCodeMessages(database, target.AgentID)
	}
	f, err := os.Open(target.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := max(int64(0), info.Size()-(16<<20))
	data, err := io.ReadAll(io.NewSectionReader(f, start, info.Size()-start))
	if err != nil {
		return nil, err
	}
	if start > 0 {
		_, data, _ = bytes.Cut(data, []byte("\n"))
	}
	var messages []Message
	for _, line := range bytes.Split(data, []byte("\n")) {
		if message, ok := parseMessage(line, target.Tool); ok {
			messages = append(messages, message)
		}
	}
	return messages, nil
}

type messageEnvelope struct {
	Role      string          `json:"role"`
	Type      string          `json:"type"`
	Channel   string          `json:"channel"`
	Recipient string          `json:"recipient"`
	Content   json.RawMessage `json:"content"`
	Text      string          `json:"text"`
	Synthetic bool            `json:"synthetic"`
	Ignored   bool            `json:"ignored"`
}

func parseMessage(line []byte, tool string) (Message, bool) {
	var row struct {
		Type      string          `json:"type"`
		IsMeta    bool            `json:"isMeta"`
		IsSummary bool            `json:"isCompactSummary"`
		Message   messageEnvelope `json:"message"`
		Payload   messageEnvelope `json:"payload"`
	}
	if json.Unmarshal(line, &row) != nil || row.IsMeta || row.IsSummary {
		return Message{}, false
	}
	var envelope messageEnvelope
	switch tool {
	case ToolClaude:
		if row.Type != "user" && row.Type != "assistant" {
			return Message{}, false
		}
		envelope = row.Message
	case ToolCodex:
		if row.Type != "response_item" || row.Payload.Type != "message" {
			return Message{}, false
		}
		envelope = row.Payload
	default:
		return Message{}, false
	}
	return visibleMessage(envelope)
}

func visibleMessage(envelope messageEnvelope) (Message, bool) {
	if envelope.Synthetic || envelope.Ignored {
		return Message{}, false
	}
	if envelope.Role != "user" && envelope.Role != "assistant" {
		return Message{}, false
	}
	if envelope.Recipient != "" && envelope.Recipient != "all" {
		return Message{}, false
	}
	if envelope.Channel != "" && envelope.Channel != "final" && envelope.Channel != "commentary" {
		return Message{}, false
	}
	var parts []string
	var text string
	if json.Unmarshal(envelope.Content, &text) == nil && text != "" && !injectedMessage(text) {
		parts = append(parts, text)
	}
	var blocks []messageEnvelope
	if json.Unmarshal(envelope.Content, &blocks) == nil {
		for _, block := range blocks {
			if block.Synthetic || block.Ignored {
				continue
			}
			switch block.Type {
			case "text", "input_text", "output_text":
				if !injectedMessage(block.Text) {
					parts = append(parts, block.Text)
				}
			case "image", "input_image":
				if envelope.Role == "user" {
					parts = append(parts, "[Image]")
				}
			}
		}
	}
	if envelope.Text != "" && !injectedMessage(envelope.Text) {
		parts = append(parts, envelope.Text)
	}
	text = strings.TrimSpace(strings.Join(parts, "\n\n"))
	return Message{Role: envelope.Role, Text: text}, text != ""
}

func injectedMessage(text string) bool {
	if proseKind(KindUser, text) == KindSystem {
		return true
	}
	for _, prefix := range []string{"# AGENTS.md instructions for ", "<environment_context>", "<turn_aborted>", "<subagent_notification>", "<teammate-message", "<instructions>", "<permissions instructions>"} {
		if strings.HasPrefix(strings.TrimSpace(text), prefix) {
			return true
		}
	}
	return false
}

func readOpenCodeMessages(path, id string) ([]Message, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	layout, err := opencode.Layout(db)
	if err != nil || layout != opencode.SchemaV2 {
		return nil, err
	}
	query := `SELECT id, data, type FROM (SELECT id, data, type, time_created FROM session_message
		WHERE session_id = ? ORDER BY time_created DESC, id DESC LIMIT 512) ORDER BY time_created, id`
	rows, err := db.Query(query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []Message
	lastID := ""
	for rows.Next() {
		var messageID, data, meta string
		if err := rows.Scan(&messageID, &data, &meta); err != nil {
			return nil, err
		}
		var envelope messageEnvelope
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			return nil, err
		}
		envelope.Role = meta
		if message, ok := visibleMessage(envelope); ok {
			if messageID == lastID {
				messages[len(messages)-1].Text += "\n\n" + message.Text
			} else {
				messages = append(messages, message)
				lastID = messageID
			}
		}
	}
	return messages, rows.Err()
}
