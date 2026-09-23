package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
)

// The board's revive relaunches a row on the account it already holds, with
// no Select to record who is borrowing it.
func TestTheBoardsReviveOnAPooledAccountRecordsTheBorrower(t *testing.T) {
	t.Cleanup(accounts.UsePool(accountstest.LoggedInAs("owner").Resolve))
	m := buildModel(t)
	withAccountTools(m)
	seedAccountRow(t, m, "worker01", "worker", "claude", "")
	if err := m.store.SetAccount("worker01", "ALICE1"); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)
	sess, ok := m.sessionByID("worker01")
	if !ok || sess.Account != "ALICE1" {
		t.Fatalf("seeded row = %+v", sess)
	}
	if err := m.reviveSession(sess); err != nil {
		t.Fatalf("revive: %v", err)
	}
	if got, _ := m.store.Setting("account_borrower:worker01"); got != "OWNER" {
		t.Fatalf("account_borrower:worker01 = %q, want OWNER", got)
	}
}
