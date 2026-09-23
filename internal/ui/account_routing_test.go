package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestSettingsPersistsSubscriptionRouting(t *testing.T) {
	t.Cleanup(accounts.UsePool(accountstest.LoggedInAs("owner").Resolve))
	m := buildModel(t)
	if err := m.store.SetSetting(store.DefaultAccountSetting, "WRONG_LEGACY_OWNER"); err != nil {
		t.Fatal(err)
	}
	m.openSettings()
	rendered := ansi.Strip(m.viewSettings())
	if strings.Contains(rendered, "WRONG_LEGACY_OWNER") || strings.Contains(rendered, "default account") {
		t.Fatalf("manual ownership picker remains: %s", rendered)
	}
	if m.settings.accountRouting != accounts.Own {
		t.Fatal(m.settings.accountRouting)
	}
	m.settings.field = settingsFieldAccountRouting
	m.cycleSetting(1)
	if !strings.Contains(ansi.Strip(m.viewSettings()), "smart routing") {
		t.Fatal("missing routing mode")
	}
	m.persistSettings()
	if mode, err := m.store.Setting(store.AccountRoutingSetting); err != nil || mode != accounts.Smart {
		t.Fatalf("%q %v", mode, err)
	}
	m.openSettings()
	if m.settings.accountRouting != accounts.Smart {
		t.Fatal("mode not restored")
	}
	m.settings.field = settingsFieldAccountRouting
	m.cycleSetting(-1)
	m.persistSettings()
	if mode, _ := accounts.Mode(m.store); mode != accounts.Own {
		t.Fatal(mode)
	}
}

// With no pool, smart routing is not offered, and a smart mode left by a
// build that had one is shown and saved as own.
func TestSettingsWithNoPoolOffersOnlyOwnSubscription(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting(store.AccountRoutingSetting, accounts.Smart); err != nil {
		t.Fatal(err)
	}
	m.openSettings()
	m.settings.field = settingsFieldAccountRouting
	m.cycleSetting(1)
	rendered := ansi.Strip(m.viewSettings())
	if strings.Contains(rendered, "smart routing") || !strings.Contains(rendered, "no account pool") {
		t.Fatalf("routing row offers smart routing with no pool: %s", rendered)
	}
	m.persistSettings()
	if mode, err := accounts.Mode(m.store); err != nil || mode != accounts.Own {
		t.Fatalf("saved %q %v, want own", mode, err)
	}
}
