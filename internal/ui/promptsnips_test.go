package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/promptsnips"
	"github.com/usestring/gate-inbox/internal/search"
)

func TestPromptSnipsLoadBothSubmissionLogs(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	codexHome := filepath.Join(root, "codex")
	for _, dir := range []string{claudeHome, codexHome} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	usePromptHistoryRoots(t, claudeHome, codexHome)
	now := time.Now()
	var claude, codex strings.Builder
	for i := range 4 {
		fmt.Fprintf(&claude, "{\"display\":\"Review the pull request\",\"timestamp\":%d,\"sessionId\":\"c%d\"}\n", now.UnixMilli()+int64(i), i)
		fmt.Fprintf(&codex, "{\"text\":\"Run the full test suite\",\"ts\":%d,\"session_id\":\"x%d\"}\n", now.Unix()+int64(i), i)
	}
	for path, content := range map[string]string{
		filepath.Join(claudeHome, "history.jsonl"): claude.String(),
		filepath.Join(codexHome, "history.jsonl"):  codex.String(),
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	msg := loadPromptSnips().(promptSnipsLoadedMsg)
	if msg.err != nil || len(msg.snips) != 2 {
		t.Fatalf("loaded %v snippets, want both tools", msg.snips)
	}
	m := &Model{}
	_, cmd := m.update(msg)
	if m.promptSnips == nil || cmd == nil {
		t.Fatal("loaded snippets should be cached and schedule a refresh")
	}
	log, err := os.OpenFile(filepath.Join(codexHome, "history.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		fmt.Fprintf(log, "{\"text\":\"Explain the failing check\",\"ts\":%d,\"session_id\":\"new%d\"}\n", now.Unix()+int64(i), i)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	_, refresh := m.update(promptSnipsTickMsg{})
	if refresh == nil {
		t.Fatal("refresh tick did not start a background read")
	}
	m.update(refresh())
	if got := len(m.promptSnips); got != 3 {
		t.Fatalf("refresh found %d suggestions, want the new recurring prompt", got)
	}
}

func TestNewSessionPromptSuggestionAcceptsEditableTextForBothTools(t *testing.T) {
	now := time.Now()
	var subs []promptsnips.Submission
	for i := range 4 {
		subs = append(subs,
			promptsnips.Submission{ID: "claude" + fmt.Sprint(i), Text: "Review the pull request", At: now},
			promptsnips.Submission{ID: "codex" + fmt.Sprint(i), Text: "Review the tests", At: now},
		)
	}
	for _, tool := range []string{"claude", "codex"} {
		t.Run(tool, func(t *testing.T) {
			m := buildModel(t)
			m.openForm()
			m.form.toolNames = []string{tool}
			m.form.toolIndex = 0
			focusFormPrompt(t, m)
			m.promptSnips = promptsnips.Build(subs, now)
			m.form.prompt.input.SetValue("Review the")
			if line := m.viewForm(); !strings.Contains(line, "^Y") {
				t.Fatalf("form has no visible insertion hint: %q", line)
			}
			m.handleFormKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
			if m.form.prompt.suggestionIndex != 1 {
				t.Fatal("next suggestion was not selected")
			}
			m.handleFormKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			if m.form.prompt.suggestionIndex != 0 {
				t.Fatal("previous suggestion was not selected")
			}
			m.handleFormKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
			m.handleFormKey(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
			if got := m.form.prompt.input.Value(); got != "Review the tests" {
				t.Fatalf("accepted %q", got)
			}
			if m.mode != modeForm || len(m.sessions) != 0 {
				t.Fatal("accepting a suggestion launched or closed the form")
			}
			m.handleFormKey(tea.KeyPressMsg{Code: '!', Text: "!"})
			if got := m.form.prompt.message(); got != "Review the tests!" {
				t.Fatalf("accepted text is not editable: %q", got)
			}
			m.formFocus(1)
			if strings.Contains(m.viewForm(), "^Y") {
				t.Fatal("suggestions remain visible outside the prompt field")
			}
		})
	}
}

func TestPromptHistoryErrorSurfacesAndRefreshes(t *testing.T) {
	root := t.TempDir()
	usePromptHistoryRoots(t, root, root)
	if err := os.Mkdir(filepath.Join(root, "history.jsonl"), 0700); err != nil {
		t.Fatal(err)
	}
	msg := loadPromptSnips().(promptSnipsLoadedMsg)
	if msg.err == nil {
		t.Fatal("unreadable history was silently accepted")
	}
	m := &Model{}
	_, cmd := m.update(msg)
	if !strings.Contains(m.errBar.text, "reading prompt history") || cmd == nil {
		t.Fatal("history failure must surface and schedule a retry")
	}
}

func TestPromptSuggestionDoesNotReplaceImages(t *testing.T) {
	c := newComposer("Review [Image #1]")
	c.attachments = []imageAttachment{{id: 1}}
	snips := promptsnips.Build([]promptsnips.Submission{
		{Text: "Review the tests"}, {Text: "Review the tests"},
		{Text: "Review the tests"}, {Text: "Review the tests"},
	}, time.Now())
	if _, handled := c.suggestionKey(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl}, snips); handled {
		t.Fatal("history suggestion intercepted a prompt with images")
	}
}

func usePromptHistoryRoots(t *testing.T, claudeHome, codexHome string) {
	t.Helper()
	before := promptHistoryRoots
	promptHistoryRoots = func() *search.Locator {
		return search.NewLocator(claudeHome, filepath.Join(codexHome, "sessions"))
	}
	t.Cleanup(func() { promptHistoryRoots = before })
}
