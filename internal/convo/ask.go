package convo

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"
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
	// Preview is the markdown a dialog draws beside the option when it is
	// focused, clipped there to the pane; the call carries it whole.
	Preview string `json:"preview,omitempty"`
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
	call, ok := PendingAskCall(path)
	return call.Questions, ok
}

// AskCall is a pending AskUserQuestion call: its tool_use id, its questions
// and when the model wrote it.
type AskCall struct {
	ToolUseID string
	Questions []AskQuestion
	AskedAt   time.Time
}

// PendingAskCall is the AskUserQuestion call path's conversation is waiting
// on, with the id and time PendingAsk leaves out.
func PendingAskCall(path string) (AskCall, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return AskCall{}, false
	}
	start := max(info.Size()-askWindow, 0)
	raw, err := readRange(path, start, info.Size())
	if err != nil {
		return AskCall{}, false
	}
	lines := bytes.Split(raw, []byte{'\n'})
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	var pending AskCall
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
				pending = AskCall{ToolUseID: id, Questions: asked, AskedAt: recordTime(rec.Timestamp)}
			}
		case "user":
			for _, result := range toolResults(rec.Message.Content) {
				if pending.ToolUseID != "" && result.ToolUseID == pending.ToolUseID {
					pending = AskCall{}
				}
			}
		}
	}
	if pending.ToolUseID == "" || len(pending.Questions) == 0 {
		return AskCall{}, false
	}
	return pending, true
}

// PendingAskFile is the pending call the ask-pending hook saved, for a
// transcript that does not hold it yet: Claude Code 2.1.286 writes an
// AskUserQuestion call to the transcript only once it is answered, so while
// its dialog stands only the PreToolUse hook has seen it. The saved call
// counts while the transcript records no result for it.
func PendingAskFile(transcript, saved string) (AskCall, bool) {
	if transcript != "" {
		if call, ok := PendingAskCall(transcript); ok {
			return call, true
		}
	}
	raw, err := os.ReadFile(saved)
	if err != nil {
		return AskCall{}, false
	}
	var call AskCall
	if json.Unmarshal(raw, &call) != nil || call.ToolUseID == "" || len(call.Questions) == 0 {
		return AskCall{}, false
	}
	if transcript != "" && answeredIn(transcript, call.ToolUseID) {
		return AskCall{}, false
	}
	return call, true
}

// answeredIn reports whether path records a tool_result for id.
func answeredIn(path, id string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	start := max(info.Size()-askWindow, 0)
	raw, err := readRange(path, start, info.Size())
	if err != nil {
		return false
	}
	return bytes.Contains(raw, []byte(`"tool_use_id":"`+id+`"`))
}

// AnsweredAsk is an AskUserQuestion call and the answers its dialog returned.
type AnsweredAsk struct {
	ToolUseID  string
	Questions  []AskQuestion
	Answers    map[string]string
	AskedAt    time.Time
	AnsweredAt time.Time
}

// AnsweredAsks is every AskUserQuestion call in path answered after since,
// oldest first. The questions come from the call the model wrote and the
// answers from the result Claude Code recorded (toolUseResult.answers, keyed
// by question text); a result with no call before it in the file is skipped.
func AnsweredAsks(path string, since time.Time) ([]AnsweredAsk, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 1<<20)
	calls := map[string]AnsweredAsk{}
	var out []AnsweredAsk
	for {
		line, err := reader.ReadBytes('\n')
		if bytes.Contains(line, []byte("AskUserQuestion")) || bytes.Contains(line, []byte(`"answers"`)) {
			collectAsk(bytes.TrimSpace(line), calls, since, &out)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

func collectAsk(line []byte, calls map[string]AnsweredAsk, since time.Time, out *[]AnsweredAsk) {
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var rec struct {
		record
		ToolUseResult json.RawMessage `json:"toolUseResult"`
	}
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	at := recordTime(rec.Timestamp)
	switch rec.Type {
	case "assistant":
		if id, asked, found := askCall(rec.Message.Content); found {
			calls[id] = AnsweredAsk{ToolUseID: id, Questions: asked, AskedAt: at}
		}
	case "user":
		var result struct {
			Answers map[string]string `json:"answers"`
		}
		if len(rec.ToolUseResult) == 0 || rec.ToolUseResult[0] != '{' || json.Unmarshal(rec.ToolUseResult, &result) != nil {
			return
		}
		for _, tr := range toolResults(rec.Message.Content) {
			call, found := calls[tr.ToolUseID]
			if !found || len(result.Answers) == 0 {
				continue
			}
			delete(calls, tr.ToolUseID)
			if at.IsZero() || !at.After(since) {
				continue
			}
			call.Answers, call.AnsweredAt = result.Answers, at
			*out = append(*out, call)
		}
	}
}

func recordTime(stamp string) time.Time {
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}
	}
	return at
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
