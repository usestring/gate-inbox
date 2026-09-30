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
