// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/search"
)

const (
	jevEndpoint       = "https://api.typesafe.ai/v1/systemone"
	jevModel          = "jev-1.13.0"
	autoSuggestDelay  = 500 * time.Millisecond
	autoSuggestLimit  = 8
	autoSuggestMinLen = 12
)

var jevHTTPClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}

type suggestMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type suggestInput struct {
	Messages   []suggestMessage
	Draft      string
	Candidates []string
}

type autoSuggestTickMsg struct{ seq int }

type autoSuggestResultMsg struct {
	seq         int
	identity    string
	draft       string
	suggestions []string
}

func boundedText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func nextSubmissionCandidates(messages []search.Message, input string) []string {
	firstUser, lastUser := -1, -1
	for i, message := range messages {
		if message.Role == "user" {
			if firstUser < 0 {
				firstUser = i
			}
			lastUser = i
		}
	}
	if firstUser < 0 || firstUser == lastUser {
		return nil
	}
	prefix := strings.ToLower(strings.TrimSpace(input))
	seen := map[string]bool{}
	var candidates []string
	for i := lastUser - 1; i > firstUser && len(candidates) < autoSuggestLimit; i-- {
		if messages[i].Role != "user" {
			continue
		}
		value := strings.TrimSpace(messages[i].Text)
		if len([]rune(value)) < autoSuggestMinLen || len([]rune(value)) > 300 || strings.Contains(value, "\n") {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(value), " "))
		if seen[key] || !strings.HasPrefix(key, prefix) || strings.EqualFold(value, input) {
			continue
		}
		seen[key] = true
		candidates = append(candidates, value)
	}
	return candidates
}

func newSuggestInput(messages []search.Message, draft string) suggestInput {
	input := suggestInput{Draft: draft}
	if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
		input.Candidates = nextSubmissionCandidates(messages, draft)
	}
	for i := len(messages) - 1; i >= 0 && len(input.Messages) < 4; i-- {
		message := messages[i]
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		text := boundedText(message.Text, 500)
		if text != "" {
			input.Messages = append(input.Messages, suggestMessage{Role: message.Role, Text: text})
		}
	}
	slices.Reverse(input.Messages)
	return input
}

func rankNextSubmissions(ctx context.Context, client *http.Client, endpoint, key string, input suggestInput) []string {
	if key == "" || len(input.Candidates) == 0 {
		return nil
	}
	if len(input.Messages) > 4 {
		input.Messages = input.Messages[len(input.Messages)-4:]
	}
	for i := range input.Messages {
		input.Messages[i].Text = boundedText(input.Messages[i].Text, 500)
	}
	input.Draft = boundedText(input.Draft, 2000)
	input.Candidates = input.Candidates[:min(len(input.Candidates), autoSuggestLimit)]
	for _, candidate := range input.Candidates {
		if len([]rune(candidate)) > 300 || strings.Contains(candidate, "\n") {
			return nil
		}
	}
	criteria := map[string]string{"none": "None of these is a useful next user submission for this active session."}
	for i, candidate := range input.Candidates {
		criteria["c"+string(rune('1'+i))] = candidate
	}
	body, err := json.Marshal(struct {
		Model     string `json:"model"`
		State     any    `json:"state"`
		Questions any    `json:"questions"`
	}{
		Model: jevModel,
		State: map[string]any{"messages": input.Messages, "draft": input.Draft},
		Questions: map[string]any{"next": map[string]any{
			"type":         "choice",
			"instructions": "Choose the candidate that would be the most useful next user submission to the existing session after its latest assistant message. Choose none if these are old instructions that should not be repeated or the draft already says something better.",
			"criteria":     criteria,
		}},
	})
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var reply struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Type   string `json:"type"`
			Choice string `json:"choice"`
		} `json:"answers"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply) != nil || reply.Model != jevModel {
		return nil
	}
	answer := reply.Answers["next"]
	if answer.Type != "choice" {
		return nil
	}
	choice := answer.Choice
	if choice == "none" {
		return nil
	}
	for i, candidate := range input.Candidates {
		if choice == "c"+string(rune('1'+i)) {
			return []string{candidate}
		}
	}
	return nil
}

func (m *Model) suggestIdentity() (string, bool) {
	if !m.jevAutoSuggest || !m.quick.active || m.conversation == nil || os.Getenv("TYPESAFE_API_KEY") == "" {
		return "", false
	}
	entry, ok := m.selectedRow()
	if !ok || entry.isGroup || m.conversation.key != m.conversationIdentity(entry.sess) {
		return "", false
	}
	return m.conversation.key + "\x00" + m.conversation.stamp, true
}

func (m *Model) scheduleAutoSuggestion() tea.Cmd {
	identity, ok := m.suggestIdentity()
	if !ok {
		m.quick.suggestions = nil
		m.quick.suggestionIdentity = ""
		m.quick.suggestionDraft = ""
		m.autoSuggestSeq++
		return nil
	}
	draft := m.quick.input.Value()
	if m.quick.suggestionIdentity == identity && m.quick.suggestionDraft == draft {
		return nil
	}
	m.quick.suggestionIdentity, m.quick.suggestionDraft = identity, draft
	m.quick.suggestions = nil
	m.autoSuggestSeq++
	seq := m.autoSuggestSeq
	return tea.Tick(autoSuggestDelay, func(time.Time) tea.Msg { return autoSuggestTickMsg{seq: seq} })
}

func (m *Model) runAutoSuggestion(seq int) tea.Cmd {
	identity, ok := m.suggestIdentity()
	if !ok || seq != m.autoSuggestSeq {
		return nil
	}
	draft := m.quick.input.Value()
	input := newSuggestInput(m.conversation.messages, draft)
	if len(input.Candidates) == 0 {
		return nil
	}
	key := os.Getenv("TYPESAFE_API_KEY")
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		return autoSuggestResultMsg{seq: seq, identity: identity, draft: draft,
			suggestions: rankNextSubmissions(ctx, jevHTTPClient, jevEndpoint, key, input)}
	}
}

func (m *Model) applyAutoSuggestion(msg autoSuggestResultMsg) {
	identity, ok := m.suggestIdentity()
	if ok && msg.seq == m.autoSuggestSeq && msg.identity == identity && msg.draft == m.quick.input.Value() {
		m.quick.suggestions = msg.suggestions
	}
}

func (m *Model) autoSuggestions() []string {
	if identity, ok := m.suggestIdentity(); ok && identity == m.quick.suggestionIdentity && m.quick.input.Value() == m.quick.suggestionDraft {
		return m.quick.suggestions
	}
	return nil
}

func (m *Model) selectedAutoSuggestion() string {
	candidates := m.autoSuggestions()
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}
