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
	calls, next, err := pendingCalls(path)
	if err != nil || calls == nil {
		return "", next, err
	}
	for candidate, call := range calls {
		if name != "" && call.name != name {
			continue
		}
		if id != "" {
			return "", next, nil
		}
		id = candidate
	}
	return id, next, nil
}

// PendingToolStrings is every string the input of the one unresolved tool
// call holds -- the command of a Bash call, the path of an Edit, the URL of a
// fetch -- or nil when no single call is pending. A permission prompt asks
// about exactly that call, and its own words for it are wrapped to the pane.
func PendingToolStrings(path string) []string {
	calls, _, err := pendingCalls(path)
	if err != nil || len(calls) != 1 {
		return nil
	}
	var fields map[string]any
	for _, call := range calls {
		if json.Unmarshal(call.input, &fields) != nil {
			return nil
		}
	}
	var out []string
	for _, value := range fields {
		if text, ok := value.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

type pendingToolCall struct {
	name  string
	input json.RawMessage
}

// pendingCalls is every unresolved tool call in the transcript's tail, or nil
// when the tail does not reach back to a typed prompt and so cannot tell.
func pendingCalls(path string) (map[string]pendingToolCall, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	start := max(int64(0), info.Size()-deltaCap)
	raw, err := readRange(path, start, info.Size())
	if err != nil {
		return nil, 0, err
	}
	end := bytes.LastIndexByte(raw, '\n') + 1
	next := start + int64(end)
	lines := bytes.Split(raw[:end], []byte{'\n'})
	if start > 0 {
		lines = lines[1:]
	}
	pending := map[string]pendingToolCall{}
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
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(rec.Message.Content, &parts) != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == "tool_use" && part.ID != "" {
				pending[part.ID] = pendingToolCall{name: part.Name, input: part.Input}
			}
		}
	}
	if !complete {
		return nil, next, nil
	}
	return pending, next, nil
}
