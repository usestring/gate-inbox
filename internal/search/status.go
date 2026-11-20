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
	t := unwrapPasted(text)
	switch {
	case strings.HasPrefix(t, "Auto wake-up:"):
		return TurnNudge
	case !band.Has(t) || strings.HasPrefix(t, band.Tag+" From "):
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
				if next, ok := statusReportInput(block.Input); ok {
					report, found = next, true
				}
			}
		}
	}
	return report, found, nil
}

// statusReportInput reads one report_status call's input as the tool
// served it. The server accepts status and summary for state and note
// (mcpserver.argAliases), and the transcript keeps the names the model
// sent, so a call the server recorded under an alias has to read the same
// way here; the schema's own name wins when both are present.
func statusReportInput(raw json.RawMessage) (StatusReport, bool) {
	var input struct {
		State   string `json:"state"`
		Note    string `json:"note"`
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return StatusReport{}, false
	}
	report := StatusReport{State: input.State, Note: input.Note}
	if report.State == "" {
		report.State = input.Status
	}
	if report.Note == "" {
		report.Note = input.Summary
	}
	return report, report.State != ""
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
