package ui

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
)

// accountRecipe points the claude tool at a secret store answering with
// command, for the session's account checks.
func accountRecipe(m *Model, command string) {
	withAccountTools(m)
	claude := m.cfg.Tools["claude"]
	claude.AccountCommand = command
	m.cfg.Tools["claude"] = claude
}

// The board's own revive goes through the same check unpark does: a row
// whose account's secret was disabled comes back on the chooser's pick,
// written to the row, and the status line says so.
func TestTheBoardsReviveMovesOffAnUnavailableAccount(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "lane-lead", t.TempDir(), "")
	sess := m.sessionRows()[0]
	if sess.Tool != "claude" {
		t.Skipf("the first tool is %s, not claude", sess.Tool)
	}
	accountRecipe(m, "case {secret} in *CAROL3) echo 'ERROR: (gcloud.secrets.versions.access) FAILED_PRECONDITION: Secret Version [projects/1/secrets/{secret}/versions/2] is in DISABLED state.' >&2; exit 1;; *) echo sk-test;; esac")
	chooser := &accountstest.Chooser{Account: "BOB2"}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	if err := m.store.SetAccount(sess.ID, "CAROL3"); err != nil {
		t.Fatal(err)
	}
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "lane-lead")
	m.reviveSelected()
	if !m.tmux.Exists(sess.ID) {
		t.Fatalf("not revived: %q", m.errBar.text)
	}
	if row, err := m.store.Get(sess.ID); err != nil || row.Account != "BOB2" {
		t.Fatalf("row account = %q (%v)", row.Account, err)
	}
	if !strings.Contains(m.errBar.text, "account CAROL3 is unavailable; relaunched on BOB2") {
		t.Fatalf("errBar = %q", m.errBar.text)
	}
}

// A restart settles the account before it ends the agent, so a store that
// refuses every read (the operator's access, not the account) leaves the
// running agent alone instead of killing it and failing the launch.
func TestTheBoardsRestartKeepsTheAgentWhenTheAccountCannotBeUsed(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "lane-lead", t.TempDir(), "")
	sess := m.sessionRows()[0]
	if sess.Tool != "claude" {
		t.Skipf("the first tool is %s, not claude", sess.Tool)
	}
	accountRecipe(m, `echo "ERROR: (gcloud.secrets.versions.access) PERMISSION_DENIED: Permission 'secretmanager.versions.access' denied for resource '{secret}'" >&2; exit 1`)
	chooser := &accountstest.Chooser{Account: "BOB2"}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	if err := m.store.SetAccount(sess.ID, "CAROL3"); err != nil {
		t.Fatal(err)
	}
	sess.Account = "CAROL3"
	err := m.restartSession(sess)
	if err == nil || !strings.Contains(err.Error(), "every other account tried") {
		t.Fatalf("restart err = %v", err)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("restart killed the agent before finding no account to launch on")
	}
	if row, err := m.store.Get(sess.ID); err != nil || row.Account != "CAROL3" {
		t.Fatalf("row account = %q (%v)", row.Account, err)
	}
}
