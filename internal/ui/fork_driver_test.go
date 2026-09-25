package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tooldrivers/tooldriverstest"
)

func TestExpandForkCommandQuotesTheSessionFile(t *testing.T) {
	got := expandForkCommand("tool --load {session_file}", "source", "/store/it's.json", "", "")
	if want := `tool --load '/store/it'\''s.json'`; got != want {
		t.Fatalf("fork command = %q, want %q", got, want)
	}
}

// A CLI that forks by loading a file names no {id}: the driver its
// session_store names finds the file, and the fork launches on it.
func TestForkLoadsTheSourceFromTheDriversSessionFile(t *testing.T) {
	asked := ""
	tooldriverstest.Install(t, &tooldriverstest.Driver{
		Name: "fake",
		File: func(id string) (string, error) {
			asked = id
			return "/store/" + id + ".json", nil
		},
	})
	m := buildModel(t)
	source := store.Session{
		ID:             "source-file",
		Name:           "source",
		Tool:           "codex",
		Cwd:            t.TempDir(),
		Status:         status.Idle,
		AgentSessionID: "source-conversation",
	}
	if err := m.store.CreateSession(source); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.selectSessionRow(t, "source")

	tool := m.cfg.Tools["codex"]
	tool.ForkCommand = "true {session_file}; cat"
	tool.SessionStore = "fake"
	tool.MCP = "none"
	m.cfg.Tools["codex"] = tool

	m.openFork()
	if m.errBar.text != "" {
		t.Fatalf("openFork error = %q", m.errBar.text)
	}
	m.fork.name.SetValue("forked")
	updated, cmd := m.handleForkKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	m.applyCmd(t, cmd)
	if m.mode != modeFocus || m.errBar.text != "" {
		t.Fatalf("after fork: mode=%v err=%q", m.mode, m.errBar.text)
	}
	if asked != "source-conversation" {
		t.Fatalf("the driver was asked for %q", asked)
	}
}
