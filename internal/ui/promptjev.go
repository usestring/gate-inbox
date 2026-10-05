package ui

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/promptsnips"
)

// promptJevLimit caps how many recurring prompts one ranking request
// discloses, and promptJevShown how many suggestions the form line cycles.
const (
	promptJevLimit = 8
	promptJevShown = 3
	promptJevChars = 300
)

type promptJevTickMsg struct{ seq int }

type promptJevResultMsg struct {
	seq      int
	identity string
	choice   string
}

// promptJevState is JEV's pick among the New Session suggestions for one
// draft. identity names the draft and candidate set the pick was asked for,
// and choice is the chosen snippet's Key, empty until JEV answers.
type promptJevState struct {
	seq      int
	identity string
	choice   string
}

// promptJevCandidates is what a ranking request may send: the draft and
// whitespace-collapsed recurring prompts, every one cut to promptJevChars.
func promptJevCandidates(snips []promptsnips.Snippet, draft string, now time.Time) (string, []promptsnips.Snippet, []string) {
	matches := promptsnips.Suggest(snips, draft, now, promptJevLimit)
	texts := make([]string, len(matches))
	for i, snip := range matches {
		texts[i] = boundedText(strings.Join(strings.Fields(snip.Text), " "), promptJevChars)
	}
	return boundedText(draft, promptJevChars), matches, texts
}

func promptJevIdentity(draft string, matches []promptsnips.Snippet) string {
	keys := make([]string, len(matches))
	for i, snip := range matches {
		keys[i] = snip.Key
	}
	slices.Sort(keys)
	return promptsnips.Normalize(draft) + "\x00" + strings.Join(keys, "\x00")
}

// promptJevInput is the request the New Session prompt would make now. It
// needs both Prompt suggestions and JEV Auto Suggest on and a key, and at
// least two candidates, since ranking one discloses it for nothing.
func (m *Model) promptJevInput() (identity, draft string, texts []string, matches []promptsnips.Snippet, ok bool) {
	if !m.promptSuggest || !m.jevAutoSuggest || m.mode != modeForm || m.form.focus != fieldPrompt ||
		len(m.form.prompt.attachments) > 0 || m.jevAPIKey() == "" {
		return "", "", nil, nil, false
	}
	value := m.form.prompt.input.Value()
	draft, matches, texts = promptJevCandidates(m.promptSnips, value, time.Now())
	if len(matches) < 2 {
		return "", "", nil, nil, false
	}
	return promptJevIdentity(value, matches), draft, texts, matches, true
}

func (m *Model) schedulePromptJev() tea.Cmd {
	identity, _, _, _, ok := m.promptJevInput()
	if !ok {
		if m.promptJev.identity != "" {
			m.promptJev = promptJevState{seq: m.promptJev.seq + 1}
		}
		return nil
	}
	if identity == m.promptJev.identity {
		return nil
	}
	m.promptJev = promptJevState{seq: m.promptJev.seq + 1, identity: identity}
	seq := m.promptJev.seq
	return tea.Tick(autoSuggestDelay, func(time.Time) tea.Msg { return promptJevTickMsg{seq: seq} })
}

func (m *Model) runPromptJev(seq int) tea.Cmd {
	identity, draft, texts, matches, ok := m.promptJevInput()
	if !ok || seq != m.promptJev.seq || identity != m.promptJev.identity {
		return nil
	}
	key := m.jevAPIKey()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		msg := promptJevResultMsg{seq: seq, identity: identity}
		if i := rankNewSessionPrompts(ctx, jevHTTPClient, jevEndpoint, key, draft, texts); i >= 0 {
			msg.choice = matches[i].Key
		}
		return msg
	}
}

func rankNewSessionPrompts(ctx context.Context, client *http.Client, endpoint, key, draft string, texts []string) int {
	return askJev(ctx, client, endpoint, key, map[string]any{"draft": draft},
		"Choose the candidate that best completes the draft as the first prompt for a new coding agent session. Choose none if no candidate fits the draft.",
		"None of these completes the draft.", texts)
}

func (m *Model) applyPromptJev(msg promptJevResultMsg) {
	if msg.seq == m.promptJev.seq && msg.identity == m.promptJev.identity {
		m.promptJev.choice = msg.choice
	}
}

// promptJevChoice is JEV's pick for the draft as it stands, or empty when
// the draft or its candidates changed since JEV was asked.
func (m *Model) promptJevChoice() string {
	if m.promptJev.choice == "" {
		return ""
	}
	if identity, _, _, _, ok := m.promptJevInput(); !ok || identity != m.promptJev.identity {
		return ""
	}
	return m.promptJev.choice
}
