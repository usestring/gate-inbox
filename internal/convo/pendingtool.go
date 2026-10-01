package convo

import (
	"bytes"
	"encoding/json"
	"os"
)

// PendingTool snapshots the unique unresolved call of name, or any tool when
// name is empty. Ambiguous calls cannot identify the dialog being answered.
// The cursor ends at a whole record, including when the file is mid-write.
func PendingTool(path, name string) (id string, next int64, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	start := max(int64(0), info.Size()-deltaCap)
	raw, err := readRange(path, start, info.Size())
	if err != nil {
		return "", 0, err
	}
	end := bytes.LastIndexByte(raw, '\n') + 1
	next = start + int64(end)
	lines := bytes.Split(raw[:end], []byte{'\n'})
	if start > 0 {
		lines = lines[1:]
	}
	pending := map[string]string{}
	complete := start == 0
	for _, line := range lines {
		var rec record
		if json.Unmarshal(line, &rec) != nil || rec.IsMeta {
			continue
		}
		if rec.Type == "user" {
			if typedPrompt(rec.Message.Content) != "" {
				clear(pending)
				complete = true
			}
			for _, result := range toolResults(rec.Message.Content) {
				delete(pending, result.ToolUseID)
			}
		}
		if rec.Type != "assistant" {
			continue
		}
		var parts []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(rec.Message.Content, &parts) != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == "tool_use" && part.ID != "" {
				pending[part.ID] = part.Name
			}
		}
	}
	if !complete {
		return "", next, nil
	}
	for candidate, tool := range pending {
		if name != "" && tool != name {
			continue
		}
		if id != "" {
			return "", next, nil
		}
		id = candidate
	}
	return id, next, nil
}
