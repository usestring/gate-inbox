package search

import (
	"encoding/json"
	"strings"

	"github.com/usestring/gate-inbox/internal/band"
)

// Turn is what a user turn asks of the agent, as far as judging whether its
// work is finished goes.
type Turn int

const (
	// TurnWork is a prompt that sets or changes the work: one the user
	// typed, or a message from another agent.
	TurnWork Turn = iota
	// TurnNudge is the manager telling the agent to carry on with what it
	// was doing. The prompt says nothing about the work; the reply is the
	// work itself.
	TurnNudge
	// TurnChore is the manager asking for something beside the work, such as
	// a prompt-cache keep-alive. Neither it nor its reply says anything
	// about whether the work is finished.
	TurnChore
)

// ClassifyTurn reads a user turn's text. A message typed in as pasted
// content keeps its wrapper in the transcript, so the tag is looked for
// inside it too.
func ClassifyTurn(text string) Turn {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "<pasted_content") {
		if _, rest, ok := strings.Cut(t, ">"); ok {
			t = strings.TrimSpace(rest)
		}
	}
	switch {
	case strings.HasPrefix(t, "Auto wake-up:"):
		return TurnNudge
	case !band.Has(t):
		return TurnWork
	case strings.Contains(t, "Continue the interrupted task") ||
		strings.Contains(t, "Then continue.") ||
		strings.Contains(t, "then carry on with what you were doing."):
		return TurnNudge
	case strings.Contains(t, "not a task") || strings.Contains(t, "This request is from Gate Inbox itself"):
		return TurnChore
	}
	return TurnWork
}

// WorkMessages drops the turns that say nothing about the work: chores with
// their replies, and the nudge prompts (their replies are kept). What is
// left reads the same before and after a keep-alive, so a verdict about it
// holds across one.
func WorkMessages(messages []Message) []Message {
	var out []Message
	chore := false
	for _, message := range messages {
		if message.Role == "user" {
			kind := ClassifyTurn(message.Text)
			chore = kind == TurnChore
			if kind != TurnWork {
				continue
			}
		}
		if message.Role == "assistant" && chore {
			continue
		}
		out = append(out, message)
	}
	return out
}

// StatusReport is what an agent said of its own work through the
// report_status tool.
type StatusReport struct {
	State string `json:"state"`
	Note  string `json:"note"`
}

// ReadStatusReport returns the last report_status call an agent made since
// the last prompt that set its work. The transcript is the record, not a
// file the tool writes: a report stays bound to the turn it was made in,
// lapses the moment the user or another agent asks for more, and survives
// keep-alives and wake-ups in between. Claude transcripts only.
func ReadStatusReport(target Target) (StatusReport, bool, error) {
	if target.Tool != ToolClaude {
		return StatusReport{}, false, nil
	}
	lines, err := tailLines(target.Path, 16<<20)
	if err != nil {
		return StatusReport{}, false, err
	}
	var report StatusReport
	found := false
	for _, line := range lines {
		var row struct {
			Type    string `json:"type"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &row) != nil || row.IsMeta {
			continue
		}
		switch row.Type {
		case "user":
			if promptSetsWork(row.Message.Content) {
				report, found = StatusReport{}, false
			}
		case "assistant":
			var blocks []struct {
				Type  string          `json:"type"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if json.Unmarshal(row.Message.Content, &blocks) != nil {
				continue
			}
			for _, block := range blocks {
				if block.Type != "tool_use" || !strings.HasSuffix(block.Name, "__report_status") {
					continue
				}
				var next StatusReport
				if json.Unmarshal(block.Input, &next) == nil && next.State != "" {
					report, found = next, true
				}
			}
		}
	}
	return report, found, nil
}

// promptSetsWork reports a user row holding input from a person or another
// agent. Tool results ride in user rows too and set nothing.
func promptSetsWork(content json.RawMessage) bool {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return !injectedMessage(text) && ClassifyTurn(text) == TurnWork
	}
	var blocks []messageEnvelope
	if json.Unmarshal(content, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "image" || block.Type == "input_image" {
			return true
		}
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" && !injectedMessage(block.Text) && ClassifyTurn(block.Text) == TurnWork {
			return true
		}
	}
	return false
}
