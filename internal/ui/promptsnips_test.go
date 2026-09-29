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
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	t.Setenv("CODEX_HOME", codexHome)
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
	if !msg.ok || len(msg.snips) != 2 {
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

func TestPromptSuggestionAcceptsEditableTextInBothComposers(t *testing.T) {
	now := time.Now()
	var subs []promptsnips.Submission
	for i := range 4 {
		subs = append(subs,
			promptsnips.Submission{ID: "claude" + fmt.Sprint(i), Text: "Review the pull request", At: now},
			promptsnips.Submission{ID: "codex" + fmt.Sprint(i), Text: "Review the tests", At: now},
		)
	}
	for _, target := range []composerID{composerQuick, composerForm} {
		m := &Model{promptSnips: promptsnips.Build(subs, now)}
		*m.composerFor(target) = *newComposer("Review the")
		c := m.composerFor(target)
		if line := m.promptSuggestionLine(c, 100); !strings.Contains(line, "^Y") {
			t.Fatalf("target %v has no visible insertion hint: %q", target, line)
		}
		if _, ok := m.composerKey(target, tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}); !ok {
			t.Fatalf("target %v did not select next suggestion", target)
		}
		if _, ok := m.composerKey(target, tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl}); !ok {
			t.Fatalf("target %v did not accept suggestion", target)
		}
		if got := c.input.Value(); got != "Review the tests" {
			t.Fatalf("target %v inserted %q", target, got)
		}
		c.typeKey(tea.KeyPressMsg{Code: '!', Text: "!"})
		if got := c.message(); got != "Review the tests!" {
			t.Fatalf("target %v did not leave editable text: %q", target, got)
		}
	}
}
