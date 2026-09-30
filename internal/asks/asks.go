// Package asks reads the questions an agent CLI has put to its user out of
// that CLI's own structured record: Claude Code's transcript, Codex's rollout,
// OpenCode's session store.
package asks

import (
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
)

type Target struct {
	Tool           string
	AgentSessionID string
	Cwd            string
	ClaudeHome     string
}

type Call struct {
	Tool      string
	ID        string
	AskedAt   time.Time
	Questions []convo.AskQuestion
	Async     bool
}

type Outcome int

const (
	Unresolved Outcome = iota
	Answered
	Expired
	Dismissed
)

func (o Outcome) String() string {
	switch o {
	case Answered:
		return "answered"
	case Expired:
		return "expired"
	case Dismissed:
		return "dismissed"
	}
	return "unresolved"
}

type Result struct {
	Call    Call
	Outcome Outcome
	Answers []Registered
	// Reply is the message that answered a call asked without a dialog.
	Reply string
	At    time.Time
}

type Registered struct {
	Labels []string
	Note   string
}

func (r Registered) Text() string {
	text := strings.Join(r.Labels, ", ")
	if r.Note == "" {
		return text
	}
	if text == "" {
		return r.Note
	}
	return text + " (" + r.Note + ")"
}

type Traits struct {
	Name                  string
	MultiSelectAnswerable bool
	FreeText              string
	Expires               time.Duration
}

type Source interface {
	Traits() Traits
	Located(t Target) bool
	Pending(t Target) (Call, bool)
	Result(t Target, id string) (Result, bool)
	Answered(t Target, since time.Time) ([]convo.AnsweredAsk, error)
	Unanswered(t Target) (Result, bool)
}

var (
	mu      sync.RWMutex
	sources = map[string]Source{"claude": claude{}}
)

func Register(tool string, src Source) {
	mu.Lock()
	defer mu.Unlock()
	sources[tool] = src
}

func For(tool string) (Source, bool) {
	mu.RLock()
	defer mu.RUnlock()
	src, ok := sources[tool]
	return src, ok
}

func Pending(t Target) (Call, bool) {
	src, ok := For(t.Tool)
	if !ok || t.AgentSessionID == "" {
		return Call{}, false
	}
	return src.Pending(t)
}

func PendingQuestions(t Target) []convo.AskQuestion {
	call, _ := Pending(t)
	return call.Questions
}

func ResultOf(t Target, id string) (Result, bool) {
	src, ok := For(t.Tool)
	if !ok || t.AgentSessionID == "" || id == "" {
		return Result{}, false
	}
	return src.Result(t, id)
}

func AnsweredSince(t Target, since time.Time) ([]convo.AnsweredAsk, error) {
	src, ok := For(t.Tool)
	if !ok || t.AgentSessionID == "" {
		return nil, nil
	}
	return src.Answered(t, since)
}

func Located(t Target) bool {
	src, ok := For(t.Tool)
	return ok && t.AgentSessionID != "" && src.Located(t)
}

func Unanswered(t Target) (Result, bool) {
	src, ok := For(t.Tool)
	if !ok || t.AgentSessionID == "" {
		return Result{}, false
	}
	return src.Unanswered(t)
}

func TraitsOf(tool string) Traits {
	if src, ok := For(tool); ok {
		return src.Traits()
	}
	return claude{}.Traits()
}

func Wait(t Target, id string, timeout, every time.Duration) (Result, bool) {
	deadline := time.Now().Add(timeout)
	for {
		result, ok := ResultOf(t, id)
		if ok && result.Outcome != Unresolved {
			return result, true
		}
		if time.Now().After(deadline) {
			return result, ok
		}
		time.Sleep(every)
	}
}

type claude struct{}

func (claude) Traits() Traits {
	return Traits{Name: "Claude Code", FreeText: "type instead"}
}

func (c claude) Located(t Target) bool { return c.transcript(t) != "" }

func (claude) transcript(t Target) string {
	home := t.ClaudeHome
	if home == "" {
		home = convo.ClaudeHome()
	}
	return convo.TranscriptFor(home, t.AgentSessionID, t.Cwd)
}

func (c claude) Pending(t Target) (Call, bool) {
	path := c.transcript(t)
	if path == "" {
		return Call{}, false
	}
	call, ok := convo.PendingAskCall(path)
	if !ok {
		return Call{}, false
	}
	return Call{Tool: "claude", ID: call.ToolUseID, AskedAt: call.AskedAt, Questions: call.Questions}, true
}

func (c claude) Result(t Target, id string) (Result, bool) {
	path := c.transcript(t)
	if path == "" {
		return Result{}, false
	}
	answered, err := convo.AnsweredAsks(path, time.Time{})
	if err != nil {
		return Result{}, false
	}
	for _, ask := range answered {
		if ask.ToolUseID != id {
			continue
		}
		result := Result{
			Call:    Call{Tool: "claude", ID: id, AskedAt: ask.AskedAt, Questions: ask.Questions},
			Outcome: Answered,
			At:      ask.AnsweredAt,
		}
		for _, q := range ask.Questions {
			result.Answers = append(result.Answers, Registered{Labels: []string{ask.Answers[q.Question]}})
		}
		return result, true
	}
	if call, ok := convo.PendingAskCall(path); ok && call.ToolUseID == id {
		return Result{Call: Call{Tool: "claude", ID: id, AskedAt: call.AskedAt, Questions: call.Questions}}, true
	}
	return Result{}, false
}

func (c claude) Answered(t Target, since time.Time) ([]convo.AnsweredAsk, error) {
	path := c.transcript(t)
	if path == "" {
		return nil, nil
	}
	return convo.AnsweredAsks(path, since)
}

func (claude) Unanswered(Target) (Result, bool) { return Result{}, false }
