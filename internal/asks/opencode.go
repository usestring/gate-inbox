package asks

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/opencode"

	_ "modernc.org/sqlite"
)

type opencodeSource struct{}

func init() { Register("opencode", opencodeSource{}) }

func (opencodeSource) Traits() Traits {
	return Traits{Name: "OpenCode", MultiSelectAnswerable: true, FreeText: "type as its own answer instead"}
}

// OpencodeQuestionPart is one question tool call OpenCode stored in its
// session store, in stored order.
type OpencodeQuestionPart struct {
	ID        string
	MessageAt time.Time
	CreatedAt time.Time
	Status    string
	Questions []convo.AskQuestion
	Custom    []bool
	Answers   [][]string
	ErrorText string
	UpdatedAt time.Time
}

type opencodePart struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	State struct {
		Status string `json:"status"`
		Input  struct {
			Questions []struct {
				Header   string            `json:"header"`
				Question string            `json:"question"`
				Multiple bool              `json:"multiple"`
				Custom   *bool             `json:"custom"`
				Options  []convo.AskOption `json:"options"`
			} `json:"questions"`
		} `json:"input"`
		Metadata struct {
			Answers [][]string `json:"answers"`
		} `json:"metadata"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"state"`
	Time struct {
		Created   int64 `json:"created"`
		Ran       int64 `json:"ran"`
		Completed int64 `json:"completed"`
	} `json:"time"`
}

// OpencodeQuestions reads every question call of the session id out of the
// store at path, oldest first, and says whether a user message was stored
// after the last of them.
func OpencodeQuestions(path, id string) ([]OpencodeQuestionPart, bool, error) {
	if path == "" || id == "" {
		return nil, false, nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, false, nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	rows, err := db.Query(`SELECT type, time_created, time_updated, data FROM session_message
		WHERE session_id = ? AND (type = 'user' OR (type = 'assistant' AND data LIKE '%"question"%'))
		ORDER BY time_created, seq`, id)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var (
		out       []OpencodeQuestionPart
		userAfter bool
	)
	for rows.Next() {
		var (
			kind             string
			created, updated int64
			data             string
		)
		if err := rows.Scan(&kind, &created, &updated, &data); err != nil {
			return out, userAfter, err
		}
		if kind == "user" {
			userAfter = len(out) > 0
			continue
		}
		var message struct {
			Content []opencodePart `json:"content"`
		}
		if json.Unmarshal([]byte(data), &message) != nil {
			continue
		}
		for _, part := range message.Content {
			if part.Type != "tool" || part.Name != "question" {
				continue
			}
			stored := OpencodeQuestionPart{ID: part.ID, MessageAt: time.UnixMilli(created), Status: part.State.Status,
				Answers: part.State.Metadata.Answers, ErrorText: part.State.Error.Message, UpdatedAt: time.UnixMilli(updated)}
			stored.CreatedAt = time.UnixMilli(part.Time.Created)
			if part.Time.Completed > 0 {
				stored.UpdatedAt = time.UnixMilli(part.Time.Completed)
			}
			for _, q := range part.State.Input.Questions {
				stored.Questions = append(stored.Questions, convo.AskQuestion{Header: q.Header, Question: q.Question,
					MultiSelect: q.Multiple, Options: q.Options})
				stored.Custom = append(stored.Custom, q.Custom == nil || *q.Custom)
			}
			out = append(out, stored)
			userAfter = false
		}
	}
	return out, userAfter, rows.Err()
}

func (opencodeSource) parts(t Target) ([]OpencodeQuestionPart, bool) {
	parts, userAfter, _ := OpencodeQuestions(opencode.DBPath(), t.AgentSessionID)
	return parts, userAfter
}

func (s opencodeSource) Located(t Target) bool {
	path := opencode.DBPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil && strings.HasPrefix(t.AgentSessionID, "ses_")
}

func opencodeCall(part OpencodeQuestionPart) Call {
	return Call{Tool: "opencode", ID: part.ID, AskedAt: part.CreatedAt, Questions: part.Questions}
}

func opencodeResult(part OpencodeQuestionPart) Result {
	result := Result{Call: opencodeCall(part), At: part.UpdatedAt}
	switch part.Status {
	case "completed":
		result.Outcome = Answered
		for i := range part.Questions {
			var labels []string
			if i < len(part.Answers) {
				labels = part.Answers[i]
			}
			result.Answers = append(result.Answers, Registered{Labels: labels})
		}
	case "error":
		result.Outcome = Dismissed
	default:
		result.At = time.Time{}
	}
	return result
}

func (s opencodeSource) Pending(t Target) (Call, bool) {
	parts, _ := s.parts(t)
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i].Status == "running" || parts[i].Status == "pending" {
			return opencodeCall(parts[i]), true
		}
	}
	return Call{}, false
}

func (s opencodeSource) Result(t Target, id string) (Result, bool) {
	parts, _ := s.parts(t)
	for _, part := range parts {
		if part.ID == id {
			return opencodeResult(part), true
		}
	}
	return Result{}, false
}

func (s opencodeSource) Unanswered(t Target) (Result, bool) {
	parts, userAfter := s.parts(t)
	if len(parts) == 0 || userAfter {
		return Result{}, false
	}
	last := parts[len(parts)-1]
	if last.Status != "error" {
		return Result{}, false
	}
	return opencodeResult(last), true
}

func (s opencodeSource) Answered(t Target, since time.Time) ([]convo.AnsweredAsk, error) {
	parts, _ := s.parts(t)
	var out []convo.AnsweredAsk
	for _, part := range parts {
		if part.Status != "completed" || !part.UpdatedAt.After(since) {
			continue
		}
		answered := convo.AnsweredAsk{ToolUseID: part.ID, Questions: part.Questions, AskedAt: part.CreatedAt,
			AnsweredAt: part.UpdatedAt, Answers: map[string]string{}}
		for i, q := range part.Questions {
			if i < len(part.Answers) {
				answered.Answers[q.Question] = strings.Join(part.Answers[i], ", ")
			}
		}
		out = append(out, answered)
	}
	return out, nil
}
