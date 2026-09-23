package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// forkWithDialog runs the fork key over a source session whose tool prints a
// resume dialog and records every keystroke sent back to it. It returns the
// model and the file the pane's keystrokes land in.
func forkWithDialog(t *testing.T, keys []string) (*Model, string) {
	t.Helper()
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("work", dir); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "source", dir, "work")
	m.selectSessionRow(t, "source")
	source := m.rows[m.cursor].sess
	if err := m.store.SetAgentSessionID(source.ID, "source-conversation"); err != nil {
		t.Fatal(err)
	}
	for i := range m.sessions {
		if m.sessions[i].ID == source.ID {
			m.sessions[i].AgentSessionID = "source-conversation"
		}
	}
	m.rebuildRows()
	m.selectSessionRow(t, "source")

	keysFile := filepath.Join(t.TempDir(), "keys")
	tool := m.cfg.Tools[source.Tool]
	// The two options the real dialog draws, then a reader that keeps the
	// pane alive and writes what it is sent where the test can read it.
	tool.ForkCommand = `sh -c 'printf "1. Resume from summary (recommended)\n2. Resume full session as-is\n"; cat > ` + tmux.ShellQuote(keysFile) + `' {id}`
	tool.ForkDialogOption = "Resume full session as-is"
	tool.ForkDialogKeys = keys
	m.cfg.Tools[source.Tool] = tool

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = updated.(*Model)
	if m.mode != modeFork {
		t.Fatalf("f left mode %v, err %q", m.mode, m.errBar.text)
	}
	m.fork.name.SetValue("child fork")
	updated, cmd := m.handleForkKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if m.errBar.text != "" || m.mode != modeFocus {
		t.Fatalf("fork reported %q (mode %v)", m.errBar.text, m.mode)
	}
	m.applyCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("the pass after the fork reported %q", m.errBar.text)
	}
	m.leaveFocusForFixture(t)
	return m, keysFile
}

func forkedRowID(t *testing.T, m *Model) string {
	t.Helper()
	for _, sess := range m.sessionRows() {
		if sess.Name == "child fork" {
			return sess.ID
		}
	}
	t.Fatal("forked session not found")
	return ""
}

func TestForkAnswersTheResumeDialogWithTheFullSession(t *testing.T) {
	m, keysFile := forkWithDialog(t, []string{"Down", "Enter"})
	forked := forkedRowID(t, m)

	// The pane has to paint the dialog before a pass can see it, so the
	// polls that find nothing are part of the behaviour under test.
	var sent []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.applyCmd(t, m.refreshCmd())
		if raw, err := os.ReadFile(keysFile); err == nil && len(raw) > 0 {
			sent = raw
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(sent) == 0 {
		t.Fatal("no keys reached the forked pane")
	}
	// Down arrives as a cursor-key escape sequence; Enter is what submits it
	// and is why the reader saw the line at all.
	if !strings.Contains(string(sent), "\x1b") {
		t.Fatalf("keys sent to the pane = %q, want a cursor key", sent)
	}
	m.poller.mu.Lock()
	_, armed := m.poller.forkDialogs[forked]
	m.poller.mu.Unlock()
	if armed {
		t.Fatal("answered dialog is still armed")
	}
}

func TestForkWithoutDialogKeysArmsNothing(t *testing.T) {
	m, keysFile := forkWithDialog(t, nil)
	forked := forkedRowID(t, m)
	m.poller.mu.Lock()
	_, armed := m.poller.forkDialogs[forked]
	m.poller.mu.Unlock()
	if armed {
		t.Fatal("a tool with no fork_dialog_keys armed an answer")
	}
	for i := 0; i < 3; i++ {
		m.applyCmd(t, m.refreshCmd())
	}
	if raw, err := os.ReadFile(keysFile); err == nil && len(raw) > 0 {
		t.Fatalf("keys reached an unarmed pane: %q", raw)
	}
}

func TestForkDialogWaitsForTheOptionOnScreen(t *testing.T) {
	// No tmux driver: a send would panic, which is the point -- an option
	// that is not on screen must not produce one.
	p := &poller{forkDialogs: map[string]forkDialog{
		"fork": {option: "Resume full session as-is", keys: []string{"Down", "Enter"}, expiry: time.Now().Add(time.Minute)},
	}}
	if err := p.maybeAnswerForkDialog(store.Session{ID: "fork", Name: "child fork"}, "❯ still booting"); err != nil {
		t.Fatal(err)
	}
	if _, armed := p.forkDialogs["fork"]; !armed {
		t.Fatal("expectation dropped before the dialog appeared")
	}
	if err := p.maybeAnswerForkDialog(store.Session{ID: "other", Name: "other"}, "2. Resume full session as-is"); err != nil {
		t.Fatal(err)
	}
}

func TestForkDialogExpires(t *testing.T) {
	p := &poller{forkDialogs: map[string]forkDialog{
		"fork": {option: "Resume full session as-is", keys: []string{"Down", "Enter"}, expiry: time.Now().Add(-time.Second)},
	}}
	if err := p.maybeAnswerForkDialog(store.Session{ID: "fork", Name: "child fork"}, "2. Resume full session as-is"); err != nil {
		t.Fatal(err)
	}
	if _, armed := p.forkDialogs["fork"]; armed {
		t.Fatal("expired expectation survived")
	}
}
