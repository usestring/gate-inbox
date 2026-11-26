package sessioncmd

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// disabledAccountConfig is sessionConfig with an account recipe that
// answers, for CAROL3's secret, what gcloud prints for a disabled version.
var disabledAccountConfig = strings.Replace(sessionConfig,
	`account_command = "echo test-token-{secret}"`,
	`account_command = "case {secret} in *CAROL3) echo 'ERROR: (gcloud.secrets.versions.access) FAILED_PRECONDITION: Secret Version [projects/1/secrets/{secret}/versions/2] is in DISABLED state.' >&2; exit 1;; *) echo test-token-{secret};; esac"`, 1)

// A parked session whose account's secret was disabled while it was down
// comes back on the chooser's pick, not stuck parked on the same error at
// every unpark; the row then names the account it runs on.
func TestUnparkRevivesOffAnUnavailableAccount(t *testing.T) {
	if !strings.Contains(disabledAccountConfig, "DISABLED state") {
		t.Fatal("the test config no longer has the account recipe this test replaces")
	}
	chooser := &accountstest.Chooser{Account: "alice1"}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	h := newSessionHarnessWithConfig(t, disabledAccountConfig)
	sess := store.Session{ID: uuid.NewString()[:8], Name: "lane-lead", Tool: "claude", Cwd: h.caller.Cwd, Group: h.caller.Group, Status: status.Dead, AgentSessionID: "conv-x"}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetAccount(sess.ID, "CAROL3"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetParked([]string{sess.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := h.sessions.Unpark(h.caller.ID)
	if err != nil {
		t.Fatalf("Unpark: %v", err)
	}
	if len(result.Errors) != 0 || len(result.Revived) != 1 || result.Revived[0].ID != sess.ID || result.Owed != 0 {
		t.Fatalf("unpark result = %+v", result)
	}
	if row, err := h.store.Get(sess.ID); err != nil || row.Account != "ALICE1" {
		t.Fatalf("row account = %q (%v), want the chooser's ALICE1", row.Account, err)
	}
	reqs := chooser.Requests()
	if len(reqs) != 1 || reqs[0].Reason != extension.LaunchRelaunch || reqs[0].SessionID != sess.ID || strings.Join(reqs[0].Exclude, ",") != "CAROL3" {
		t.Fatalf("requests = %+v", reqs)
	}
}

// With no chooser in the build there is nothing to pick another account,
// so the session stays parked with the one-line reason, for a retry.
func TestUnparkWithNoChooserKeepsAnUnavailableAccountParked(t *testing.T) {
	t.Cleanup(accounts.UseChooser(func() (extension.AccountChooser, error) { return nil, nil }))
	h := newSessionHarnessWithConfig(t, disabledAccountConfig)
	sess := store.Session{ID: uuid.NewString()[:8], Name: "lane-lead", Tool: "claude", Cwd: h.caller.Cwd, Group: h.caller.Group, Status: status.Dead, AgentSessionID: "conv-x"}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetAccount(sess.ID, "CAROL3"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetParked([]string{sess.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := h.sessions.Unpark(h.caller.ID)
	if err != nil {
		t.Fatalf("Unpark: %v", err)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "account CAROL3 is unavailable: its secret version is disabled") || result.Owed != 1 {
		t.Fatalf("unpark result = %+v", result)
	}
}

// parkOnCarol3 is a dead session on CAROL3 in the parked set.
func parkOnCarol3(t *testing.T, h *sessionHarness) store.Session {
	t.Helper()
	sess := store.Session{ID: uuid.NewString()[:8], Name: "lane-lead", Tool: "claude", Cwd: h.caller.Cwd, Group: h.caller.Group, Status: status.Dead, AgentSessionID: "conv-x"}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetAccount(sess.ID, "CAROL3"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetParked([]string{sess.ID}); err != nil {
		t.Fatal(err)
	}
	return sess
}

// A chooser with nothing to offer brings the session back on the CLI's own
// login, says so in the result, and leaves the row on CAROL3, so the next
// relaunch checks it again rather than staying on the own login for good.
func TestUnparkOnTheOwnLoginWarnsAndKeepsTheRowsAccount(t *testing.T) {
	chooser := &accountstest.Chooser{}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	h := newSessionHarnessWithConfig(t, disabledAccountConfig)
	sess := parkOnCarol3(t, h)
	result, err := h.sessions.Unpark(h.caller.ID)
	if err != nil {
		t.Fatalf("Unpark: %v", err)
	}
	if len(result.Errors) != 0 || len(result.Revived) != 1 || result.Owed != 0 {
		t.Fatalf("unpark result = %+v", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "account CAROL3 is unavailable: its secret version is disabled") || !strings.Contains(result.Warnings[0], "own login") {
		t.Fatalf("warnings = %q", result.Warnings)
	}
	if revived := result.Revived[0]; revived.Account != "" || !strings.Contains(FormatUnparkResult(result), "own login") {
		t.Fatalf("revived %+v, text %q", revived, FormatUnparkResult(result))
	}
	if row, err := h.store.Get(sess.ID); err != nil || row.Account != "CAROL3" {
		t.Fatalf("row account = %q (%v), want CAROL3 kept", row.Account, err)
	}
}

// refusedAccountConfig refuses every secret read, as the store does when
// the operator's own credentials lose access.
var refusedAccountConfig = strings.Replace(sessionConfig,
	`account_command = "echo test-token-{secret}"`,
	`account_command = "echo \"ERROR: (gcloud.secrets.versions.access) PERMISSION_DENIED: Permission 'secretmanager.versions.access' denied for resource '{secret}'\" >&2; exit 1"`, 1)

// When the row's account and the chooser's pick are both refused, the fault
// is the operator's access, and the session stays parked with the error
// rather than coming back on the own login.
func TestUnparkKeepsTheSessionParkedWhenEveryReadIsRefused(t *testing.T) {
	if !strings.Contains(refusedAccountConfig, "PERMISSION_DENIED") {
		t.Fatal("the test config no longer has the account recipe this test replaces")
	}
	chooser := &accountstest.Chooser{Account: "alice1"}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	h := newSessionHarnessWithConfig(t, refusedAccountConfig)
	sess := parkOnCarol3(t, h)
	result, err := h.sessions.Unpark(h.caller.ID)
	if err != nil {
		t.Fatalf("Unpark: %v", err)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "every other account tried") || result.Owed != 1 || len(result.Warnings) != 0 {
		t.Fatalf("unpark result = %+v", result)
	}
	if row, err := h.store.Get(sess.ID); err != nil || row.Account != "CAROL3" {
		t.Fatalf("row account = %q (%v)", row.Account, err)
	}
}

// revive_session says the same in its result line.
func TestReviveOnTheOwnLoginSaysSo(t *testing.T) {
	chooser := &accountstest.Chooser{}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	h := newSessionHarnessWithConfig(t, disabledAccountConfig)
	sess := parkOnCarol3(t, h)
	revived, err := h.sessions.Revive(h.caller.ID, sess.ID)
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	if revived.Account != "" || !strings.Contains(FormatSession(revived), "account CAROL3 is unavailable") {
		t.Fatalf("revived %+v, text %q", revived, FormatSession(revived))
	}
	if row, err := h.store.Get(sess.ID); err != nil || row.Account != "CAROL3" {
		t.Fatalf("row account = %q (%v)", row.Account, err)
	}
}
