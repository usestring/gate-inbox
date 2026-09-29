package sessioncmd

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// These set the process's account chooser, so none of them is parallel.

// A migration takes no account from its caller and carries none from its
// source: with no chooser it runs on the CLI's own login.
func TestMigrateDoesNotCarryTheSourcesAccount(t *testing.T) {
	t.Cleanup(accounts.UseChooser(func() (extension.AccountChooser, error) { return nil, nil }))
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)
	if err := h.store.SetAccount(source.ID, "OLD"); err != nil {
		t.Fatal(err)
	}
	moved, err := h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "claude"})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if row, err := h.store.Get(moved.ID); err != nil || row.Account != "" {
		t.Fatalf("with no chooser the move should run on own login, got %q (%v)", row.Account, err)
	}
}

// With a chooser the migration runs on its pick, whatever the launch-account
// mode, and the chooser is told it is a migration and from where.
func TestMigrateAsksTheChooser(t *testing.T) {
	chooser := &accountstest.Chooser{Account: "alice1"}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)
	moved, err := h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "claude"})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if moved.Account != "ALICE1" {
		t.Fatalf("migrated onto %q, want the chooser's ALICE1", moved.Account)
	}
	reqs := chooser.Requests()
	if len(reqs) != 1 || reqs[0].Reason != extension.LaunchMigrate || reqs[0].From != source.ID || reqs[0].SessionID != moved.ID || reqs[0].Preview {
		t.Fatalf("requests = %+v", reqs)
	}
}

// A spawn naming no account asks the chooser only in the extension mode; a
// named account is never the chooser's to change.
func TestCreateAsksTheChooserOnlyInItsMode(t *testing.T) {
	chooser := &accountstest.Chooser{Account: "alice1"}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	h := newSessionHarness(t)
	own, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "own", Tool: "claude"})
	if err != nil || own.Account != "" || len(chooser.Requests()) != 0 {
		t.Fatalf("own mode: %+v %v, asked %d", own, err, len(chooser.Requests()))
	}
	if err := h.store.SetSetting(store.AccountRoutingSetting, accounts.Extension); err != nil {
		t.Fatal(err)
	}
	chosen, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "chosen", Tool: "claude"})
	if err != nil || chosen.Account != "ALICE1" {
		t.Fatalf("extension mode: %+v %v", chosen, err)
	}
	named, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "named", Tool: "claude", Account: "bob2"})
	if err != nil || named.Account != "BOB2" || len(chooser.Requests()) != 1 {
		t.Fatalf("named: %+v %v, asked %d", named, err, len(chooser.Requests()))
	}
}

// BoardSwitchAccount re-points a session for a board extension, and refuses
// a context too large to resume rather than moving it to a new
// conversation.
func TestBoardSwitchAccount(t *testing.T) {
	h := newSessionHarness(t)
	sess := store.Session{ID: uuid.NewString()[:8], Name: "worker", Tool: "claude", Cwd: h.caller.Cwd, Group: h.caller.Group, Status: status.Dead, AgentSessionID: "conv-x"}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	account, tool, err := h.sessions.BoardAccount(sess.ID)
	if err != nil || account != "" || tool.Env != "CLAUDE_CODE_OAUTH_TOKEN" || tool.Secret == "" {
		t.Fatalf("BoardAccount = %q %+v %v", account, tool, err)
	}
	switched, err := h.sessions.BoardSwitchAccount(sess.ID, "alice1")
	if err != nil || switched.Account != "ALICE1" {
		t.Fatalf("BoardSwitchAccount = %+v %v", switched, err)
	}
	if account, _, _ := h.sessions.BoardAccount(sess.ID); account != "ALICE1" {
		t.Fatalf("account after the switch = %q", account)
	}

	big, transcript := claudeSource(t, h)
	raw := `{"type":"assistant","timestamp":"2026-09-01T00:00:00Z","message":{"model":"claude-fixture","usage":{"input_tokens":1001,"cache_read_input_tokens":198000,"cache_creation_input_tokens":1000}}}`
	if err := os.WriteFile(transcript, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.BoardSwitchAccount(big.ID, "alice1"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("a large context was not refused: %v", err)
	}
}
