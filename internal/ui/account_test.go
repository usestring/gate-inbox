package ui

import (
	"fmt"
	"github.com/usestring/gate-inbox/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// withAccountTools gives the harness's claude somewhere to take a token, and
// adds a tool an operator can still open the card on while the launch would
// refuse the account: account_env alone says a CLI takes one, and the rest of
// the block is what actually resolves it.
func withAccountTools(m *Model) {
	claude := m.cfg.Tools["claude"]
	claude.AccountEnv = "GATE_INBOX_TEST_OAUTH_TOKEN"
	claude.AccountSecret = "GATE_INBOX_TEST_SECRET_{account}"
	claude.AccountCommand = "echo sk-test"
	claude.AccountsCommand = `printf 'GATE_INBOX_TEST_SECRET_ALICE1\nGATE_INBOX_TEST_SECRET_BOB2\n'`
	m.cfg.Tools["claude"] = claude
	half := claude
	half.AccountCommand = ""
	m.cfg.Tools["half-configured"] = half
}

func seedAccountRow(t *testing.T, m *Model, id, name, tool, paneID string) {
	t.Helper()
	socket := ""
	if paneID != "" {
		socket = m.tmux.SocketName()
	}
	if err := m.store.CreateSession(store.Session{
		ID: id, Name: name, Tool: tool, Cwd: t.TempDir(),
		Status: status.Idle, CreatedAt: time.Now(), LastStatusAt: time.Now(),
		TmuxSocket: socket, TmuxPaneID: paneID,
	}); err != nil {
		t.Fatalf("CreateSession %s: %v", id, err)
	}
}

// The card is the operator's half of switch_account. It offers the accounts
// the settings card offers, and re-points a dead row without launching
// anything: the token is read at launch, so the next revive is what picks the
// new account up.
func TestTheAccountCardRepointsADeadRow(t *testing.T) {
	m := buildModel(t)
	withAccountTools(m)
	seedAccountRow(t, m, "worker01", "worker", "claude", "")
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "worker")

	m.openAccountSwitch()
	if m.mode != modeAccount {
		t.Fatalf("the card did not open: mode %v, errBar %q", m.mode, m.errBar.text)
	}
	if got := strings.Join(m.account.names, ","); got != ownLogin+",ALICE1,BOB2" {
		t.Fatalf("choices = %q", got)
	}
	if card := m.viewAccountSwitch(); !strings.Contains(card, "next revive") {
		t.Errorf("the card does not say a dead row waits for its revive:\n%s", card)
	}

	// Right twice: past ALICE1 and onto BOB2, the way the operator picks.
	m.handleAccountKey(tea.KeyPressMsg{Code: 'l', Text: "l"})
	m.handleAccountKey(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if _, _ = m.submitAccountSwitch(); m.errBar.text != "" {
		t.Fatalf("submit reported %q", m.errBar.text)
	}
	if m.mode != modeList {
		t.Errorf("the card stayed open after a switch: mode %v", m.mode)
	}
	row, err := m.store.Get("worker01")
	if err != nil || row.Account != "BOB2" {
		t.Fatalf("stored account = %q, %v", row.Account, err)
	}
	if sess, ok := m.sessionByID("worker01"); !ok || sess.Account != "BOB2" {
		t.Errorf("the loaded row still reads %q", sess.Account)
	}
	if m.tmux.Exists("worker01") {
		t.Error("re-pointing a dead row launched it")
	}

	// The picker reopens on the account the row is now on, and submitting it
	// unmoved is not a switch: the only way to apply an account is to end the
	// session, and there is nothing to apply.
	m.openAccountSwitch()
	if m.account.names[m.account.index] != "BOB2" {
		t.Fatalf("the card reopened on %q", m.account.names[m.account.index])
	}
	m.submitAccountSwitch()
	if !strings.Contains(m.errBar.text, "already on BOB2") {
		t.Errorf("errBar = %q", m.errBar.text)
	}
}

// The card refuses what the manager cannot relaunch, and switch_account now
// refuses the same things: a pane it never started, and an account the launch
// could not use. The pane is caught before the picker opens, since the answer
// does not depend on which account was picked.
func TestTheAccountCardRefusesWhatCannotBeRelaunched(t *testing.T) {
	m := buildModel(t)
	withAccountTools(m)
	seedAccountRow(t, m, "borrowed1", "borrowed", "claude", "%99")
	seedAccountRow(t, m, "halfway01", "halfway", "half-configured", "")
	m.applyCmd(t, nil)

	m.selectSessionRow(t, "borrowed")
	m.openAccountSwitch()
	if m.mode == modeAccount || !strings.Contains(m.errBar.text, "did not start") {
		t.Fatalf("an adopted pane opened the card: mode %v, errBar %q", m.mode, m.errBar.text)
	}

	m.selectSessionRow(t, "halfway")
	m.openAccountSwitch()
	if m.mode != modeAccount {
		t.Fatalf("the card did not open on a listable tool: mode %v, errBar %q", m.mode, m.errBar.text)
	}
	m.handleAccountKey(tea.KeyPressMsg{Code: 'l', Text: "l"})
	m.submitAccountSwitch()
	if !strings.Contains(m.errBar.text, "cannot be launched on a chosen account") {
		t.Fatalf("errBar = %q", m.errBar.text)
	}
	if row, _ := m.store.Get("halfway01"); row.Account != "" {
		t.Errorf("a refused account was written anyway: %q", row.Account)
	}
}

func TestAccountCardMigratesLargeContextsOnChosenSubscription(t *testing.T) {
	for _, target := range []string{"BOB2", ""} {
		t.Run("target-"+target, func(t *testing.T) {
			m := buildModel(t)
			withAccountTools(m)
			source, transcript := seedMigrateSource(t, m)
			if err := m.store.SetAccount(source.ID, "ALICE1"); err != nil {
				t.Fatal(err)
			}
			if err := m.store.SetSetting("account_borrower:"+source.ID, "ORIGINAL"); err != nil {
				t.Fatal(err)
			}
			if err := m.store.SetSetting(store.DefaultAccountSetting, "OTHER"); err != nil {
				t.Fatal(err)
			}
			raw := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fixture","usage":{"input_tokens":1001,"cache_read_input_tokens":198000,"cache_creation_input_tokens":1000}}}`, time.Now().Format(time.RFC3339Nano))
			if err := os.WriteFile(transcript, []byte(raw+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			promptFile := filepath.Join(t.TempDir(), "prompt")
			tool := m.cfg.Tools["claude"]
			tool.Command = "sh -c 'printf %s \"$1\" > " + tmux.ShellQuote(promptFile) + "; exec cat' sh"
			m.cfg.Tools["claude"] = tool
			if err := m.tmux.Create(source.ID, source.Cwd, "cat", nil, 80, 24); err != nil {
				t.Fatal(err)
			}
			loadStoredRows(t, m)
			m.selectSessionRow(t, "source")
			m.openAccountSwitch()
			if card := m.viewAccountSwitch(); !strings.Contains(card, "over 200k context") {
				t.Fatalf("missing migration notice: %s", card)
			}
			for i, name := range m.account.names {
				if name == target || (target == "" && name == ownLogin) {
					m.account.index = i
				}
			}
			_, cmd := m.submitAccountSwitch()
			if m.errBar.text != "" || m.mode != modeFocus {
				t.Fatalf("mode=%v err=%s", m.mode, m.errBar.text)
			}
			m.applyCmd(t, cmd)
			m.leaveFocusForFixture(t)
			var moved store.Session
			rows, err := m.store.ListSessions(true)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.MigrationID == source.ID {
					moved = row
				}
			}
			if moved.ID == "" || moved.AgentSessionID == source.AgentSessionID || moved.Account != target || moved.Tool != source.Tool {
				t.Fatalf("wrong migration: %+v", moved)
			}
			kept, err := m.store.Get(source.ID)
			if err != nil || kept.Account != "ALICE1" || !m.tmux.Exists(source.ID) {
				t.Fatalf("source changed: %+v %v", kept, err)
			}
			borrower, err := m.store.Setting("account_borrower:" + moved.ID)
			if err != nil || borrower != "ORIGINAL" {
				t.Fatalf("borrower=%q err=%v", borrower, err)
			}
			deadline := time.Now().Add(3 * time.Second)
			var prompt []byte
			for time.Now().Before(deadline) {
				prompt, _ = os.ReadFile(promptFile)
				if len(prompt) > 0 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !strings.Contains(string(prompt), transcript) || !strings.Contains(string(prompt), "continue the work") || strings.Contains(moved.LaunchPrompt, "--resume") {
				t.Fatalf("not a migration handover: %s", prompt)
			}
		})
	}
}

func TestAccountCardRechecksContextBeforeSwitch(t *testing.T) {
	for _, change := range []string{"shrunk", "missing", "same-account"} {
		t.Run(change, func(t *testing.T) {
			m := buildModel(t)
			withAccountTools(m)
			source, path := seedMigrateSource(t, m)
			if err := m.store.SetAccount(source.ID, "ALICE1"); err != nil {
				t.Fatal(err)
			}
			writeUsage := func(n int) {
				raw := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"usage":{"input_tokens":%d}}}`, time.Now().Format(time.RFC3339Nano), n)
				if err := os.WriteFile(path, []byte(raw+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeUsage(200001)
			loadStoredRows(t, m)
			m.selectSessionRow(t, "source")
			m.openAccountSwitch()
			if !m.account.migrate {
				t.Fatal("large context not detected")
			}
			for i, name := range m.account.names {
				if name == "BOB2" {
					m.account.index = i
				}
			}
			switch change {
			case "shrunk":
				writeUsage(200000)
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "same-account":
				for i, name := range m.account.names {
					if name == "ALICE1" {
						m.account.index = i
					}
				}
			}
			m.submitAccountSwitch()
			row, err := m.store.Get(source.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "ALICE1"
			if change == "shrunk" {
				want = "BOB2"
			}
			if row.Account != want {
				t.Fatalf("account=%s want=%s", row.Account, want)
			}
			rows, err := m.store.ListSessions(true)
			if err != nil || len(rows) != 1 {
				t.Fatalf("unexpected migration: %+v %v", rows, err)
			}
			if change == "missing" && !strings.Contains(m.errBar.text, "cannot read current context") {
				t.Fatalf("missing refusal: %q", m.errBar.text)
			}
		})
	}
}
