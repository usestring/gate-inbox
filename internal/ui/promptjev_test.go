package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/promptsnips"
)

func recurringSnips(now time.Time, texts ...string) []promptsnips.Snippet {
	var subs []promptsnips.Submission
	for n, text := range texts {
		for i := range 4 + n {
			subs = append(subs, promptsnips.Submission{ID: fmt.Sprint(n, "-", i), Text: text, At: now})
		}
	}
	return promptsnips.Build(subs, now)
}

func newSessionJevModel(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	m.promptSuggest = true
	m.openForm()
	focusFormPrompt(t, m)
	m.promptSnips = recurringSnips(time.Now(), "Review the pull request", "Review the tests", "Review the failing check")
	m.form.prompt.input.SetValue("Review the")
	return m
}

func TestNewSessionJevNeedsBothTogglesKeyAndChoices(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	m := newSessionJevModel(t)
	if cmd := m.schedulePromptJev(); cmd != nil {
		t.Fatal("JEV Auto Suggest off still queued a request")
	}
	m.jevAutoSuggest = true
	m.promptSuggest = false
	if cmd := m.schedulePromptJev(); cmd != nil {
		t.Fatal("Prompt suggestions off still queued a request")
	}
	m.promptSuggest = true
	t.Setenv("TYPESAFE_API_KEY", "")
	if cmd := m.schedulePromptJev(); cmd != nil {
		t.Fatal("missing key still queued a request")
	}
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	m.form.prompt.input.SetValue("Review the pull")
	if cmd := m.schedulePromptJev(); cmd != nil {
		t.Fatal("a single candidate was sent for ranking")
	}
	m.form.prompt.input.SetValue("Review the")
	if cmd := m.schedulePromptJev(); cmd == nil {
		t.Fatal("both toggles and a key did not queue a request")
	}
	if cmd := m.schedulePromptJev(); cmd != nil {
		t.Fatal("an unchanged draft queued a second request")
	}
	m.formFocus(1)
	if cmd := m.runPromptJev(m.promptJev.seq); cmd != nil {
		t.Fatal("a request ran after the prompt lost focus")
	}
}

func TestNewSessionJevPickLeadsAndStaysEditable(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	m := newSessionJevModel(t)
	m.jevAutoSuggest = true
	m.schedulePromptJev()
	local := m.form.prompt.suggestions(m.promptSnips, "")
	picked := local[len(local)-1]
	m.applyPromptJev(promptJevResultMsg{seq: m.promptJev.seq - 1, identity: m.promptJev.identity, choice: local[0].Key})
	if m.promptJevChoice() != "" {
		t.Fatal("a stale reply was applied")
	}
	m.applyPromptJev(promptJevResultMsg{seq: m.promptJev.seq, identity: m.promptJev.identity, choice: picked.Key})
	got := m.form.prompt.suggestions(m.promptSnips, m.promptJevChoice())
	if got[0].Key != picked.Key || len(got) != len(local) {
		t.Fatalf("JEV pick %q did not lead %v", picked.Key, got)
	}
	if line := m.promptSuggestionLine(&m.form.prompt, 120); !strings.Contains(line, "jev") {
		t.Fatalf("JEV pick is not labelled: %q", line)
	}
	m.handleFormKey(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if value := m.form.prompt.input.Value(); value != picked.Text || m.mode != modeForm || len(m.sessions) != 0 {
		t.Fatalf("accepted %q in mode %v", value, m.mode)
	}
	if m.promptJevChoice() != "" {
		t.Fatal("JEV pick outlived the draft it was asked for")
	}
	m.form.prompt.input.SetValue("Review the")
	m.jevAutoSuggest = false
	if m.promptJevChoice() != "" {
		t.Fatal("JEV pick survived turning JEV Auto Suggest off")
	}
}

func TestNewSessionJevSchedulesAfterAsyncTextPaste(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	m := newSessionJevModel(t)
	m.jevAutoSuggest = true
	m.form.prompt.input.SetValue("")
	m.form.prompt.attachments = []imageAttachment{{id: 1}}
	m.form.prompt.input.InsertString(imageToken(1))
	if cmd := m.schedulePromptJev(); cmd != nil {
		t.Fatal("pending image attachment queued ranking")
	}
	m = applyMsg(t, m, pasteImageMsg{target: composerForm, gen: m.form.prompt.gen, id: 1, noImage: true})
	_, cmd := m.Update(pasteTextMsg{
		target: composerForm,
		gen:    m.form.prompt.gen,
		inner:  tea.KeyPressMsg{Code: 'R', Text: "Review the"},
	})
	if got := m.form.prompt.input.Value(); got != "Review the" || len(m.form.prompt.attachments) != 0 {
		t.Fatalf("pasted draft = %q, attachments = %d", got, len(m.form.prompt.attachments))
	}
	identity, _, _, _, ok := m.promptJevInput()
	if !ok || m.promptJev.identity != identity || cmd == nil {
		t.Fatal("asynchronous text paste did not schedule ranking for the pasted draft")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, next := range batch {
			if tick, ok := next().(promptJevTickMsg); ok && tick.seq == m.promptJev.seq {
				return
			}
		}
	} else if tick, ok := msg.(promptJevTickMsg); ok && tick.seq == m.promptJev.seq {
		return
	}
	t.Fatal("paste command did not include the ranking debounce tick")
}

func TestNewSessionJevPayloadIsBounded(t *testing.T) {
	now := time.Now()
	var texts []string
	for i := range 10 {
		texts = append(texts, fmt.Sprintf("Review item %d\n%s", i, strings.Repeat("界", 400)))
	}
	if draft, _, _ := promptJevCandidates(nil, strings.Repeat("x", 2*promptJevChars), now); len([]rune(draft)) != promptJevChars {
		t.Fatalf("draft escaped bounds: %d runes", len([]rune(draft)))
	}
	draft, matches, sent := promptJevCandidates(recurringSnips(now, texts...), "Review item", now)
	if len(matches) != promptJevLimit || len(sent) != promptJevLimit {
		t.Fatalf("candidates = %d, want %d", len(sent), promptJevLimit)
	}
	for _, text := range sent {
		if len([]rune(text)) > promptJevChars || strings.Contains(text, "\n") {
			t.Fatalf("candidate escaped bounds: %d runes", len([]rune(text)))
		}
	}
	requests := make(chan map[string]any, 2)
	choice := "c3"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests <- request
		_ = json.NewEncoder(w).Encode(map[string]any{"model": jevModel, "answers": map[string]any{
			"next": map[string]any{"type": "choice", "choice": choice},
		}})
	}))
	defer server.Close()
	if got := rankNewSessionPrompts(context.Background(), server.Client(), server.URL, "test-key", draft, sent); got != 2 {
		t.Fatalf("ranked index = %d", got)
	}
	request := <-requests
	state := request["state"].(map[string]any)
	if len(state) != 1 || state["draft"] != "Review item" {
		t.Fatalf("state sent more than the draft: %#v", state)
	}
	if criteria := request["questions"].(map[string]any)["next"].(map[string]any)["criteria"].(map[string]any); len(criteria) != promptJevLimit+1 {
		t.Fatalf("criteria contains %d choices", len(criteria))
	}
	choice = "none"
	if got := rankNewSessionPrompts(context.Background(), server.Client(), server.URL, "test-key", draft, sent); got != -1 {
		t.Fatalf("none ranked index %d", got)
	}
	<-requests
}
