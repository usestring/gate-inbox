package sessioncmd

import (
	"testing"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// loggedInAs makes login the borrower of a pooled account.
func loggedInAs(t *testing.T, login string) {
	t.Helper()
	t.Cleanup(accounts.UsePool(accountstest.LoggedInAs(login).Resolve))
}

// pooledRow is a claude row already pointed at a pooled account with no
// borrower recorded: the state an explicit account leaves when no Select
// ran for it.
func pooledRow(t *testing.T, h *sessionHarness, live bool) store.Session {
	t.Helper()
	sess := store.Session{
		ID: uuid.NewString()[:8], Name: "pooled", Tool: "claude", Cwd: h.caller.Cwd,
		Group: h.caller.Group, Status: status.Dead, AgentSessionID: "conv-pooled", Account: "ALICE1",
	}
	if live {
		sess.Status = status.Idle
		if err := h.driver.Create(sess.ID, sess.Cwd, "", nil, 80, 24); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	return sess
}

func wantBorrower(t *testing.T, h *sessionHarness, sessionID, want string) {
	t.Helper()
	got, err := h.store.Setting("account_borrower:" + sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("account_borrower:%s = %q, want %q", sessionID, got, want)
	}
}

func TestCreateOnANamedPooledAccountRecordsTheBorrower(t *testing.T) {
	loggedInAs(t, "owner")
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "named", Tool: "claude", Account: "alice1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantBorrower(t, h, created.ID, "OWNER")
}

func TestUnparkOnAPooledAccountRecordsTheBorrower(t *testing.T) {
	loggedInAs(t, "owner")
	h := newSessionHarness(t)
	sess := pooledRow(t, h, false)
	if err := h.store.SetParked([]string{sess.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := h.sessions.Unpark(h.caller.ID)
	if err != nil || len(result.Revived) != 1 {
		t.Fatalf("Unpark: %+v, %v", result, err)
	}
	wantBorrower(t, h, sess.ID, "OWNER")
}

func TestReviveOnAPooledAccountRecordsTheBorrower(t *testing.T) {
	loggedInAs(t, "owner")
	h := newSessionHarness(t)
	sess := pooledRow(t, h, false)
	if _, err := h.sessions.Revive(h.caller.ID, sess.ID); err != nil {
		t.Fatalf("Revive: %v", err)
	}
	wantBorrower(t, h, sess.ID, "OWNER")
}

func TestSwitchAccountOntoAPooledAccountRecordsTheBorrower(t *testing.T) {
	loggedInAs(t, "owner")
	h := newSessionHarness(t)
	sess := pooledRow(t, h, true)
	if err := h.store.SetAccount(sess.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.SwitchAccount(h.caller.ID, sess.ID, "ALICE1"); err != nil {
		t.Fatalf("SwitchAccount: %v", err)
	}
	wantBorrower(t, h, sess.ID, "OWNER")
}

func TestMigrateOntoAPooledAccountRecordsTheBorrower(t *testing.T) {
	loggedInAs(t, "owner")
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)
	if err := h.store.SetAccount(source.ID, "ALICE1"); err != nil {
		t.Fatal(err)
	}
	moved, err := h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "claude"})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if moved.Account != "ALICE1" {
		t.Fatalf("migrated onto %q, want the source's pooled account", moved.Account)
	}
	wantBorrower(t, h, moved.ID, "OWNER")
}
