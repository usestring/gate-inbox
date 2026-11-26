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
