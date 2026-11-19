package asks

import (
	"bufio"
	"encoding/json"
	"os"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
)

// piSource reads the AskUserQuestion calls the board's pi extension records
// in pi's own session file: the model's toolCall in an assistant message,
// then a toolResult whose details hold the questions, the answers keyed by
// question text, and how the dialog ended. A pi manager's relayed approval
// cites the answer recorded there, as a Claude Code manager's cites its
// transcript.
type piSource struct{}

func init() { Register(convo.PiTool, piSource{}) }

func (piSource) Traits() Traits {
	return Traits{Name: "pi", MultiSelectAnswerable: true, FreeText: "type instead"}
}

func (piSource) path(t Target) string {
	return convo.PiTranscriptFor(convo.PiSessionsDir(), t.AgentSessionID, t.Cwd)
}

func (p piSource) Located(t Target) bool { return p.path(t) != "" }

// piAsk is one AskUserQuestion call and, once pi recorded it, its result.
type piAsk struct {
	call     Call
	resolved bool
	outcome  string
	answers  map[string]string
	at       time.Time
}

type piLine struct {
	Type    string `json:"type"`
	Message struct {
		Role       string `json:"role"`
		Timestamp  int64  `json:"timestamp"`
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		IsError    bool   `json:"isError"`
		Content    []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Name      string `json:"name"`
			Arguments struct {
				Questions []convo.AskQuestion `json:"questions"`
			} `json:"arguments"`
		} `json:"content"`
		Details struct {
			Questions []convo.AskQuestion `json:"questions"`
			Answers   map[string]string   `json:"answers"`
			Outcome   string              `json:"outcome"`
		} `json:"details"`
	} `json:"message"`
}

const piAskTool = "AskUserQuestion"

// asks is every AskUserQuestion call in the session file, in the order the
// model made them. A result with no call before it in the file is skipped,
// so only a call pi itself recorded can carry an answer.
func (p piSource) asks(t Target) ([]piAsk, error) {
	path := p.path(t)
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []piAsk
	index := map[string]int{}
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var entry piLine
			if json.Unmarshal(line, &entry) == nil && entry.Type == "message" {
				m := entry.Message
				switch m.Role {
				case "assistant":
					for _, c := range m.Content {
						if c.Type != "toolCall" || c.Name != piAskTool || c.ID == "" {
							continue
						}
						index[c.ID] = len(out)
						out = append(out, piAsk{call: Call{Tool: convo.PiTool, ID: c.ID,
							AskedAt: time.UnixMilli(m.Timestamp), Questions: c.Arguments.Questions}})
					}
				case "toolResult":
					i, ok := index[m.ToolCallID]
					if !ok || m.ToolName != piAskTool || out[i].resolved {
						continue
					}
					out[i].resolved, out[i].outcome, out[i].at = true, m.Details.Outcome, time.UnixMilli(m.Timestamp)
					if m.IsError {
						out[i].outcome = ""
					}
					if len(m.Details.Questions) > 0 {
						out[i].call.Questions = m.Details.Questions
					}
					if out[i].outcome == "answered" {
						out[i].answers = m.Details.Answers
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	return out, nil
}

// Pending is the call the extension's dialog is showing, which the hook
// saved before drawing it, until pi records its result.
func (p piSource) Pending(t Target) (Call, bool) {
	saved, ok := convo.PendingAskFile("", t.PendingAskFile)
	if !ok {
		return Call{}, false
	}
	all, _ := p.asks(t)
	for _, ask := range all {
		if ask.call.ID == saved.ToolUseID && ask.resolved {
			return Call{}, false
		}
	}
	return Call{Tool: convo.PiTool, ID: saved.ToolUseID, AskedAt: saved.AskedAt, Questions: saved.Questions}, true
}

func (p piSource) Result(t Target, id string) (Result, bool) {
	all, _ := p.asks(t)
	for _, ask := range all {
		if ask.call.ID != id {
			continue
		}
		result := Result{Call: ask.call, At: ask.at}
		switch {
		case !ask.resolved:
		case ask.answers != nil:
			result.Outcome = Answered
			for _, q := range ask.call.Questions {
				result.Answers = append(result.Answers, Registered{Labels: []string{ask.answers[q.Question]}})
			}
		default:
			result.Outcome = Dismissed
		}
		return result, true
	}
	return Result{}, false
}

// Answered is every call the user answered after since, oldest first.
func (p piSource) Answered(t Target, since time.Time) ([]convo.AnsweredAsk, error) {
	all, err := p.asks(t)
	if err != nil {
		return nil, err
	}
	var out []convo.AnsweredAsk
	for _, ask := range all {
		if ask.answers == nil || !ask.at.After(since) {
			continue
		}
		out = append(out, convo.AnsweredAsk{ToolUseID: ask.call.ID, Questions: ask.call.Questions,
			Answers: ask.answers, AskedAt: ask.call.AskedAt, AnsweredAt: ask.at})
	}
	return out, nil
}

func (piSource) Unanswered(Target) (Result, bool) { return Result{}, false }
