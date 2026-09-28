package convo

import (
	"bytes"
	"encoding/json"
	"os"
)

// Reading the questions a pending AskUserQuestion call is asking.
//
// The dialog Claude Code draws for a call asking several questions shows one
// at a time, under a row of tabs whose labels it cuts to fit the pane: on a
// 50-column pane "Compliance" is drawn "Com…" and the question under it is not
// on the screen at all until the tab is visited. The transcript has no such
// limit. The assistant record carrying the tool call is written before the
// dialog is drawn, with every header, question and option spelled out, and
// the tool_result answering it lands only once the dialog is submitted. So a
// call with no result after it is the dialog standing on the screen, read
// whole without a keystroke into the pane.

// AskOption is one choice an AskUserQuestion question offers.
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// AskQuestion is one question of an AskUserQuestion call, as the model wrote
// it.
type AskQuestion struct {
	Header      string      `json:"header"`
	Question    string      `json:"question"`
	MultiSelect bool        `json:"multiSelect"`
	Options     []AskOption `json:"options"`
}

// askWindow bounds how much of a transcript's end is read for a pending call.
// The call is the last thing the model wrote before the dialog went up, so it
// is near the end; the window only has to be wider than one assistant record.
const askWindow = 512 << 10

// PendingAsk is the questions of the AskUserQuestion call path's conversation
// is waiting on, or ok false when the last such call has been answered or the
// window holds none.
func PendingAsk(path string) (questions []AskQuestion, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	start := max(info.Size()-askWindow, 0)
	raw, err := readRange(path, start, info.Size())
	if err != nil {
		return nil, false
	}
	lines := bytes.Split(raw, []byte{'\n'})
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	var pendingID string
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var rec record
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		switch rec.Type {
		case "assistant":
			if id, asked, found := askCall(rec.Message.Content); found {
				pendingID, questions = id, asked
			}
		case "user":
			for _, result := range toolResults(rec.Message.Content) {
				if pendingID != "" && result.ToolUseID == pendingID {
					pendingID, questions = "", nil
				}
			}
		}
	}
	if pendingID == "" || len(questions) == 0 {
		return nil, false
	}
	return questions, true
}

// askCall reads an AskUserQuestion tool call out of one assistant record's
// content.
func askCall(content json.RawMessage) (id string, questions []AskQuestion, ok bool) {
	if len(content) == 0 || content[0] != '[' {
		return "", nil, false
	}
	var blocks []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input struct {
			Questions []AskQuestion `json:"questions"`
		} `json:"input"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return "", nil, false
	}
	for _, block := range blocks {
		if block.Type == "tool_use" && block.Name == "AskUserQuestion" && block.ID != "" {
			id, questions, ok = block.ID, block.Input.Questions, true
		}
	}
	return id, questions, ok
}
