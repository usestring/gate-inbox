// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestExpandForkCommandQuotesPlaceholders(t *testing.T) {
	got := expandForkCommand("tool --fork {id} --new {new_id} --name {name}", "source", "", "new", "Sam's fork")
	want := "tool --fork 'source' --new 'new' --name 'Sam'\\''s fork'"
	if got != want {
		t.Fatalf("fork command = %q, want %q", got, want)
	}
}

func TestForkSelectedSessionCreatesNamedSibling(t *testing.T) {
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

	argsFile := filepath.Join(t.TempDir(), "fork-args")
	tool := m.cfg.Tools[source.Tool]
	tool.ForkCommand = "printf '%s\\n' {id} {new_id} {name} > " + tmux.ShellQuote(argsFile) + "; cat"
	tool.AccountEnv = "GATE_INBOX_TEST_ACCOUNT_TOKEN"
	tool.AccountSecret = "SUB_{account}"
	tool.AccountCommand = "printf test-token"
	if err := m.store.SetSetting(store.DefaultAccountSetting, "OWNER"); err != nil {
		t.Fatal(err)
	}
	m.cfg.Tools[source.Tool] = tool

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m = updated.(*Model)
	if m.mode != modeFork || m.fork.source.ID != source.ID {
		t.Fatalf("fork mode = %v, source = %q", m.mode, m.fork.source.ID)
	}
	m.fork.name.SetValue("child fork")
	updated, cmd := m.handleForkKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	m.applyCmd(t, cmd)
	if m.mode != modeFocus || m.errBar.text != "" {
		t.Fatalf("after fork: mode=%v err=%q", m.mode, m.errBar.text)
	}
	m.leaveFocusForFixture(t)

	var forkedID string
	for _, sess := range m.sessionRows() {
		if sess.Name != "child fork" {
			continue
		}
		forkedID = sess.ID
		if sess.Account != "" {
			t.Fatalf("fork used a legacy default instead of the local login: %q", sess.Account)
		}
		if sess.Tool != source.Tool || sess.Cwd != source.Cwd || sess.Group != source.Group {
			t.Fatalf("forked session = %+v, source = %+v", sess, source)
		}
		if sess.AgentSessionID == "" || sess.AgentSessionID == source.AgentSessionID {
			t.Fatalf("forked conversation id = %q", sess.AgentSessionID)
		}
	}
	if forkedID == "" {
		t.Fatal("forked session not found")
	}
	stored, err := m.store.Get(forkedID)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var raw []byte
	for time.Now().Before(deadline) {
		raw, err = os.ReadFile(argsFile)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(raw)), "\n")
	want := []string{"source-conversation", stored.AgentSessionID, "child fork"}
	if len(got) != len(want) {
		t.Fatalf("fork args = %q", raw)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fork args = %v, want %v", got, want)
		}
	}
}

func TestOpenForkRequiresConfiguredCommandAndConversationID(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "source", t.TempDir(), "")
	m.selectSessionRow(t, "source")
	source := m.rows[m.cursor].sess

	tool := m.cfg.Tools[source.Tool]
	tool.ForkCommand = ""
	m.cfg.Tools[source.Tool] = tool
	m.openFork()
	if !strings.Contains(m.errBar.text, "no fork_command") {
		t.Fatalf("missing command error = %q", m.errBar.text)
	}

	tool.ForkCommand = "tool --fork latest"
	m.cfg.Tools[source.Tool] = tool
	m.openFork()
	if !strings.Contains(m.errBar.text, "must reference the source via {id}") {
		t.Fatalf("missing placeholder error = %q", m.errBar.text)
	}

	tool.ForkCommand = "tool --fork {id}"
	m.cfg.Tools[source.Tool] = tool
	m.openFork()
	if !strings.Contains(m.errBar.text, "no captured conversation id") {
		t.Fatalf("missing id error = %q", m.errBar.text)
	}
}

// Every fork_command the manager ships has to satisfy its own validator, so
// a placeholder can never outrun the session store that resolves it.
func TestShippedForkCommandsValidate(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools) < 4 {
		t.Fatalf("built-in tools = %d, expected every shipped CLI", len(cfg.Tools))
	}
	source := store.Session{Name: "source", AgentSessionID: "source-conversation"}
	forkable := 0
	for name, tool := range cfg.Tools {
		if tool.Shell || (tool.ForkCommand == "" && tool.SessionStore != "opencode") {
			continue
		}
		forkable++
		if err := validateForkSource(name, tool, source); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if forkable == 0 {
		t.Fatal("no shipped tool defines a fork_command")
	}
}

func TestOpenForkRejectsGroup(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("work", ""); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "work")
	m.openFork()
	if m.errBar.text != "select a session to fork" {
		t.Fatalf("group fork error = %q", m.errBar.text)
	}
}

// Tools split into two fork shapes: those that mint their own conversation id
// (opencode, codex) leave {new_id} out, so the fork starts without one until
// the session store captures it; those that take an agent-chosen id (claude)
// include {new_id} and the fork launches with it already set.
// stubOpencodeFork pins what opencode's API fork returns, so the tests do
// not depend on the box's opencode.
func stubOpencodeFork(t *testing.T, fork func(cwd, id string) (string, error)) {
	t.Helper()
	saved := forkOpencodeConversation
	forkOpencodeConversation = fork
	t.Cleanup(func() { forkOpencodeConversation = saved })
}

func TestForkAgentSessionIDFollowsNewIDPlaceholder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tool      string
		forkCmd   string
		wantNewID bool
	}{
		{"codex_session_store", "codex", "true {id}; cat", false},
		{"claude_id_flag", "claude", "true {id} {new_id}; cat", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := buildModel(t)
			dir := t.TempDir()
			source := store.Session{
				ID:             "source-" + tc.tool,
				Name:           "source",
				Tool:           tc.tool,
				Cwd:            dir,
				Status:         status.Idle,
				AgentSessionID: "source-conversation",
			}
			if err := m.store.CreateSession(source); err != nil {
				t.Fatal(err)
			}
			loadStoredRows(t, m)
			m.selectSessionRow(t, "source")

			tool := m.cfg.Tools[tc.tool]
			tool.ForkCommand = tc.forkCmd
			tool.MCP = "none"
			m.cfg.Tools[tc.tool] = tool

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
			m.leaveFocusForFixture(t)

			var forked store.Session
			for _, sess := range m.sessionRows() {
				if sess.Name == "forked" {
					forked = sess
					break
				}
			}
			if forked.ID == "" {
				t.Fatal("forked session not found")
			}
			if tc.wantNewID {
				if forked.AgentSessionID == "" || forked.AgentSessionID == source.AgentSessionID {
					t.Fatalf("forked AgentSessionID = %q, want a fresh id", forked.AgentSessionID)
				}
			} else if forked.AgentSessionID != "" {
				t.Fatalf("forked AgentSessionID = %q, want empty until the session store captures it", forked.AgentSessionID)
			}
		})
	}
}

// opencodeForkTool is the shipped opencode tool as the fork tests need it:
// a resume-by-id template and no fork_command, with cat standing in for the
// CLI so the launched pane reads what is typed into it.
func opencodeForkTool() config.Tool {
	return config.Tool{
		Command:           "cat",
		DefaultStatus:     status.Idle,
		SessionStore:      "opencode",
		ResumeByIDCommand: "true resume {id}; cat",
		MCP:               "none",
	}
}

// opencode v2 has no fork flag: the manager copies the conversation through
// opencode's API before the pane exists and resumes the copy, so the fork
// launches on its own conversation id and never needs capturing.
func TestForkOpencodeV2CopiesTheConversationAndResumesTheCopy(t *testing.T) {
	var forkedFrom []string
	stubOpencodeFork(t, func(cwd, id string) (string, error) {
		forkedFrom = append(forkedFrom, cwd+" "+id)
		return "ses_copy", nil
	})
	m := buildModel(t)
	dir := t.TempDir()
	source := store.Session{ID: "source-oc", Name: "source", Tool: "opencode", Cwd: dir, Status: status.Idle, AgentSessionID: "ses_src"}
	if err := m.store.CreateSession(source); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.selectSessionRow(t, "source")
	m.cfg.Tools["opencode"] = opencodeForkTool()

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
	m.leaveFocusForFixture(t)
	if len(forkedFrom) != 1 || forkedFrom[0] != dir+" ses_src" {
		t.Fatalf("forked from %v, want the source's directory and conversation", forkedFrom)
	}
	var forked store.Session
	for _, sess := range m.sessionRows() {
		if sess.Name == "forked" {
			forked = sess
		}
	}
	if forked.AgentSessionID != "ses_copy" {
		t.Fatalf("forked AgentSessionID = %q, want the copy the API minted", forked.AgentSessionID)
	}
}

func TestForkOpencodeV2ReportsACopyThatFailed(t *testing.T) {
	stubOpencodeFork(t, func(string, string) (string, error) { return "", errors.New("opencode: fork ses_src: exit status 1") })
	m := buildModel(t)
	m.cfg.Tools["opencode"] = opencodeForkTool()
	source := store.Session{ID: "source-oc", Name: "source", Tool: "opencode", Cwd: t.TempDir(), Status: status.Idle, AgentSessionID: "ses_src"}
	if err := m.store.CreateSession(source); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.selectSessionRow(t, "source")
	m.openFork()
	if m.errBar.text != "" {
		t.Fatalf("openFork error = %q", m.errBar.text)
	}
	m.fork.name.SetValue("forked")
	updated, _ := m.handleForkKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if !strings.Contains(m.errBar.text, "fork ses_src") {
		t.Fatalf("errBar = %q, want the API's refusal", m.errBar.text)
	}
	for _, sess := range m.sessionRows() {
		if sess.Name == "forked" {
			t.Fatal("a fork whose copy failed still got a row")
		}
	}
}

// A shell has no fork_command, but saying so names a config field for a
// row that was never going to hold a conversation.
func TestForkRefusesAShellInItsOwnTerms(t *testing.T) {
	m := buildModel(t)
	m.applyCmd(t, m.refreshCmd())
	sess := spawnTerminal(t, m)
	m.selectSessionRow(t, sess.Name)

	m.openFork()

	if m.mode == modeFork {
		t.Fatal("fork should not open on a shell row")
	}
	if !strings.Contains(m.errBar.text, "is a shell") ||
		!strings.Contains(m.errBar.text, "no conversation to fork") {
		t.Fatalf("err = %q, want it to name the row as a shell", m.errBar.text)
	}
	if strings.Contains(m.errBar.text, "fork_command") {
		t.Fatalf("err = %q should not name a config field", m.errBar.text)
	}
}
