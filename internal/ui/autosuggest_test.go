// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestRecentSubmissionAutocompleteExcludesCurrentTurn(t *testing.T) {
	messages := []search.Message{
		{Role: "user", Text: "Start the dashboard work"},
		{Role: "assistant", Text: "The layout is ready"},
		{Role: "user", Text: "Add the filtering controls"},
		{Role: "assistant", Text: "The controls are ready"},
		{Role: "user", Text: "Add the keyboard shortcuts"},
		{Role: "assistant", Text: "The shortcuts are ready"},
	}
	if got := nextSubmissionCandidates(messages, "Add the f"); !slices.Equal(got, []string{"Add the filtering controls"}) {
		t.Fatalf("autocomplete = %q", got)
	}
	if got := nextSubmissionCandidates(messages, "Add the keyboard"); len(got) != 0 {
		t.Fatalf("current submission was offered again: %q", got)
	}
}

func TestAutoSuggestIsOffAndNeverTargetsNewSessions(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	m := buildModel(t)
	createSession(t, m, "existing", t.TempDir(), "")
	m.selectSessionRow(t, "existing")
	sess, _ := m.selected()
	m.conversation.key = m.conversationIdentity(sess)
	m.conversation.messages = []search.Message{
		{Role: "user", Text: "Build the main page"},
		{Role: "assistant", Text: "Done"},
		{Role: "user", Text: "Add the search box"},
		{Role: "assistant", Text: "Done"},
		{Role: "user", Text: "Add the keyboard shortcuts"},
		{Role: "assistant", Text: "Done"},
	}
	bindMenuSnippet(t, m, menuText)
	m.openQuickMode()
	if got := m.autoSuggestions(); len(got) != 0 {
		t.Fatalf("default-off feature offered %q", got)
	}
	if cmd := m.scheduleAutoSuggestion(); cmd != nil {
		t.Fatal("default-off feature scheduled a request")
	}
	m.jevAutoSuggest = true
	if cmd := m.scheduleAutoSuggestion(); cmd == nil {
		t.Fatal("existing session did not schedule a suggestion")
	}
	m.applyAutoSuggestion(autoSuggestResultMsg{seq: m.autoSuggestSeq, identity: m.quick.suggestionIdentity,
		suggestions: []string{"Add the search box"}})
	if got := m.selectedAutoSuggestion(); got != "Add the search box" {
		t.Fatalf("existing session suggestion = %q", got)
	}
	if bar := ansi.Strip(m.viewQuickBar(80, 4)); !strings.Contains(bar, "ctrl+y insert · Add the search box") || !strings.Contains(bar, "carry on") {
		t.Fatalf("quick bar omitted the JEV suggestion: %q", bar)
	}
	var pastedID, pastedText string
	restore := pasteFocused
	pasteFocused = func(_ *tmux.Driver, id, text string) error {
		pastedID, pastedText = id, text
		return nil
	}
	t.Cleanup(func() { pasteFocused = restore })
	m.handleQuickKey(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if pastedID != sess.ID || pastedText != "Add the search box" {
		t.Fatalf("pasted %q into %q", pastedText, pastedID)
	}
	if len(m.landings) != 0 || !strings.Contains(m.errBar.text, "press enter to send") {
		t.Fatalf("suggestion was submitted instead of left for editing: %q", m.errBar.text)
	}
	if len(m.autoSuggestions()) != 0 {
		t.Fatal("accepted suggestion stayed available for duplicate insertion")
	}
	m.applyAutoSuggestion(autoSuggestResultMsg{seq: m.autoSuggestSeq, identity: m.quick.suggestionIdentity,
		suggestions: []string{"Add the search box"}})
	m.conversation.key = "other session"
	if got := m.autoSuggestions(); len(got) != 0 {
		t.Fatalf("another session's history was offered: %q", got)
	}
	m.conversation.key = m.conversationIdentity(sess)
	m.rows[m.cursor].isGroup = true
	if got := m.autoSuggestions(); len(got) != 0 {
		t.Fatalf("new-session target was offered %q", got)
	}
	if cmd := m.scheduleAutoSuggestion(); cmd != nil {
		t.Fatal("new-session target scheduled a request")
	}
}

func TestAutoSuggestWaitsForAssistantAndIgnoresStaleReply(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	m := buildModel(t)
	createSession(t, m, "existing", t.TempDir(), "")
	m.selectSessionRow(t, "existing")
	sess, _ := m.selected()
	m.conversation.key = m.conversationIdentity(sess)
	m.conversation.messages = []search.Message{
		{Role: "user", Text: "Start the dashboard work"},
		{Role: "assistant", Text: "The layout is ready"},
		{Role: "user", Text: "Add the filtering controls"},
		{Role: "assistant", Text: "The controls are ready"},
		{Role: "user", Text: "Add the keyboard shortcuts"},
	}
	m.jevAutoSuggest = true
	m.openQuickMode()
	if cmd := m.scheduleAutoSuggestion(); cmd == nil {
		t.Fatal("debounce was not scheduled")
	}
	if cmd := m.runAutoSuggestion(m.autoSuggestSeq); cmd != nil {
		t.Fatal("request was scheduled before the assistant replied")
	}
	m.conversation.messages = append(m.conversation.messages, search.Message{Role: "assistant", Text: "Shortcuts are ready"})
	m.conversation.stamp = "new"
	m.scheduleAutoSuggestion()
	old := autoSuggestResultMsg{seq: m.autoSuggestSeq - 1, identity: m.quick.suggestionIdentity, suggestions: []string{"Add the filtering controls"}}
	m.applyAutoSuggestion(old)
	if len(m.autoSuggestions()) != 0 {
		t.Fatal("stale response appeared in the quick bar")
	}
	previousSeq := m.autoSuggestSeq
	m.openQuickMode()
	m.scheduleAutoSuggestion()
	if m.autoSuggestSeq == previousSeq {
		t.Fatal("reopened quick bar reused the old request sequence")
	}
	m.applyAutoSuggestion(autoSuggestResultMsg{seq: previousSeq, identity: m.quick.suggestionIdentity,
		suggestions: []string{"Add the filtering controls"}})
	if len(m.autoSuggestions()) != 0 {
		t.Fatal("response from the previous quick bar appeared in the new one")
	}
}

func TestJevRankingBoundsPayloadAndHonorsNone(t *testing.T) {
	requests := make(chan map[string]any, 3)
	var choice atomic.Value
	choice.Store("c2")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests <- request
		_ = json.NewEncoder(w).Encode(map[string]any{"model": jevModel, "answers": map[string]any{
			"next": map[string]any{"type": "choice", "choice": choice.Load().(string)},
		}})
	}))
	defer server.Close()
	var messages []search.Message
	messages = append(messages, search.Message{Role: "user", Text: "Start the session"})
	for i := range 12 {
		messages = append(messages, search.Message{Role: "assistant", Text: strings.Repeat("界", 700)})
		messages = append(messages, search.Message{Role: "user", Text: "Add item " + strings.Repeat("x", 20) + string(rune('a'+i))})
	}
	messages = append(messages, search.Message{Role: "assistant", Text: "The latest item is ready"})
	input := newSuggestInput(messages, "")
	if len(input.Candidates) != 8 || len(input.Messages) != 4 {
		t.Fatalf("payload sizes = %d candidates, %d messages", len(input.Candidates), len(input.Messages))
	}
	got := rankNextSubmissions(context.Background(), server.Client(), server.URL, "test-key", input)
	if !slices.Equal(got, []string{input.Candidates[1]}) {
		t.Fatalf("ranked suggestion = %q", got)
	}
	request := <-requests
	state := request["state"].(map[string]any)
	for _, raw := range state["messages"].([]any) {
		if n := len([]rune(raw.(map[string]any)["text"].(string))); n > 500 {
			t.Fatalf("message contains %d characters", n)
		}
	}
	criteria := request["questions"].(map[string]any)["next"].(map[string]any)["criteria"].(map[string]any)
	if len(criteria) != 9 {
		t.Fatalf("criteria contains %d choices", len(criteria))
	}
	input.Messages = append([]suggestMessage{{Role: "user", Text: strings.Repeat("x", 600)}}, input.Messages...)
	input.Candidates = append(input.Candidates, "Add one more item", "Add the final item")
	input.Draft = strings.Repeat("x", 2200)
	_ = rankNextSubmissions(context.Background(), server.Client(), server.URL, "test-key", input)
	request = <-requests
	state = request["state"].(map[string]any)
	if len(state["messages"].([]any)) != 4 || len([]rune(state["draft"].(string))) != 2000 {
		t.Fatalf("oversized input escaped request bounds: %#v", state)
	}
	criteria = request["questions"].(map[string]any)["next"].(map[string]any)["criteria"].(map[string]any)
	if len(criteria) != 9 {
		t.Fatalf("oversized input sent %d choices", len(criteria))
	}
	choice.Store("none")
	if got := rankNextSubmissions(context.Background(), server.Client(), server.URL, "test-key", input); len(got) != 0 {
		t.Fatalf("none returned %q", got)
	}
	<-requests
}
