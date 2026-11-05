package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestSettingsPersistsTheLaunchAccountMode(t *testing.T) {
	chooser := &accountstest.Chooser{}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
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
	if !strings.Contains(ansi.Strip(m.viewSettings()), "chosen by extension") {
		t.Fatal("missing launch-account mode")
	}
	m.persistSettings()
	if mode, err := m.store.Setting(store.AccountRoutingSetting); err != nil || mode != accounts.Extension {
		t.Fatalf("%q %v", mode, err)
	}
	m.openSettings()
	if m.settings.accountRouting != accounts.Extension {
		t.Fatal("mode not restored")
	}
	m.settings.field = settingsFieldAccountRouting
	m.cycleSetting(-1)
	m.persistSettings()
	if mode, _ := accounts.Mode(m.store); mode != accounts.Own {
		t.Fatal(mode)
	}
}

// With no chooser, the extension mode is not offered, and one left by a
// build that had a chooser is shown and saved as own.
func TestSettingsWithNoChooserOffersOnlyOwnLogin(t *testing.T) {
	m := buildModel(t)
	t.Cleanup(accounts.UseChooser(func() (extension.AccountChooser, error) { return nil, nil }))
	if err := m.store.SetSetting(store.AccountRoutingSetting, accounts.Extension); err != nil {
		t.Fatal(err)
	}
	m.openSettings()
	m.settings.field = settingsFieldAccountRouting
	m.cycleSetting(1)
	rendered := ansi.Strip(m.viewSettings())
	if strings.Contains(rendered, "chosen by extension") || !strings.Contains(rendered, "no extension chooses accounts") {
		t.Fatalf("launch-account row offers the extension with no chooser: %s", rendered)
	}
	m.persistSettings()
	if mode, err := accounts.Mode(m.store); err != nil || mode != accounts.Own {
		t.Fatalf("saved %q %v, want own", mode, err)
	}
}

// The launch-accounts row steps on past the extension through every listed
// account, and the one picked runs every launch that names none, over the
// chooser.
func TestSettingsPinsEveryLaunchToAPickedAccount(t *testing.T) {
	chooser := &accountstest.Chooser{}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	m := buildModel(t)
	withAccountTools(m)
	m.openSettings()
	m.settings.field = settingsFieldAccountRouting
	m.cycleSetting(1)
	m.cycleSetting(1)
	m.cycleSetting(1)
	if got := ansi.Strip(m.viewSettings()); !strings.Contains(got, "BOB2") {
		t.Fatalf("launch accounts does not show the pinned account: %s", got)
	}
	m.persistSettings()
	if mode, err := accounts.Mode(m.store); err != nil || accounts.PinnedAccount(mode) != "BOB2" {
		t.Fatalf("saved %q %v, want BOB2 pinned", mode, err)
	}
	account, err := accounts.Select(m.store, m.cfg.Tools["claude"], "", accounts.Request{SessionID: "s1", ToolName: "claude", Reason: extension.LaunchSpawn})
	if err != nil || account != "BOB2" {
		t.Fatalf("launch ran on %q %v, want BOB2", account, err)
	}
	if named, _ := accounts.Select(m.store, m.cfg.Tools["claude"], "alice1", accounts.Request{}); named != "ALICE1" {
		t.Fatalf("a named account lost to the pin: %q", named)
	}
	if asked := len(chooser.Requests()); asked != 0 {
		t.Fatalf("chooser asked %d times with an account pinned", asked)
	}
	m.openSettings()
	if m.settings.launchAccountValue() != "BOB2" {
		t.Fatal("pinned account not restored")
	}
	m.settings.field = settingsFieldAccountRouting
	m.cycleSetting(1)
	if m.settings.accountRouting != accounts.Own {
		t.Fatalf("stepping past the last account: %q, want own login", m.settings.accountRouting)
	}
}
