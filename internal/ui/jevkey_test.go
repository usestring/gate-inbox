package ui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const testJevKey = "ts-live-0123456789abcdWXYZ"

func openJevSettings(t *testing.T, m *Model) {
	t.Helper()
	m.openSettings()
	m.settings.field = settingsFieldJev
	m.handleSettingsKey(key("enter"))
	if !m.settings.jevPanel {
		t.Fatal("the JEV row did not open the JEV card")
	}
}

func pasteJevKey(t *testing.T, m *Model, value string) {
	t.Helper()
	m.settings.jevCursor = 1
	m.handleSettingsKey(key("enter"))
	if !m.settings.jevPasting {
		t.Fatal("enter on API key did not open the key input")
	}
	m.Update(tea.PasteMsg{Content: value})
	m.handleSettingsKey(key("enter"))
}

func TestJevKeyComesFromEnvFirst(t *testing.T) {
	t.Setenv(jevKeyEnv, "")
	m := buildModel(t)
	if key, source := m.jevKey(); key != "" || source != jevKeyNone {
		t.Fatalf("no env and no saved key gave %q from %v", key, source)
	}
	if access, ok := m.jevAccess(); ok || !strings.Contains(access, "no access") {
		t.Fatalf("access without a key = %q, %v", access, ok)
	}
	m.jevSavedKey = "saved-key-0123456789"
	t.Setenv(jevKeyEnv, testJevKey)
	if key, source := m.jevKey(); key != testJevKey || source != jevKeyFromEnv {
		t.Fatalf("env key lost to %q from %v", key, source)
	}
	m.openSettings()
	if summary := m.jevSettingsSummary(); summary != "off · key from "+jevKeyEnv {
		t.Fatalf("summary = %q", summary)
	}
	t.Setenv(jevKeyEnv, "")
	if key, source := m.jevKey(); key != "saved-key-0123456789" || source != jevKeySaved {
		t.Fatalf("saved key not used without env: %q from %v", key, source)
	}
}

func TestJevKeyPastePersistsMaskedAndPrivate(t *testing.T) {
	t.Setenv(jevKeyEnv, "")
	m := buildModel(t)
	openJevSettings(t, m)
	pasteJevKey(t, m, "  "+testJevKey+"\n")
	if m.settings.jevPasting || m.jevSavedKey != testJevKey {
		t.Fatalf("pasted key not saved: pasting=%v key=%q", m.settings.jevPasting, m.jevSavedKey)
	}
	path := jevKeyPath(m.configDir())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("saved key file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v, want 0600", info.Mode().Perm())
	}
	dir, err := os.Stat(strings.TrimSuffix(path, "/"+jevKeyFile))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("key dir mode %v (%v), want 0700", dir.Mode().Perm(), err)
	}
	if stored, err := readJevKey(m.configDir()); err != nil || stored != testJevKey {
		t.Fatalf("read back %q, %v", stored, err)
	}
	card := ansi.Strip(m.viewSettings())
	if strings.Contains(card, testJevKey) || !strings.Contains(card, "••••WXYZ") || !strings.Contains(card, "access: saved key") {
		t.Fatalf("card leaks or misreports the key:\n%s", card)
	}

	restarted := buildModel(t)
	restarted.hooks = m.hooks
	restarted.loadJevKey()
	if restarted.jevAPIKey() != testJevKey {
		t.Fatalf("saved key not loaded on restart: %q", restarted.jevAPIKey())
	}

	m.handleSettingsKey(key("x"))
	if m.jevSavedKey != "" {
		t.Fatal("x did not remove the saved key")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("key file survived removal: %v", err)
	}
}

func TestJevKeyInputMasksWhileTyping(t *testing.T) {
	t.Setenv(jevKeyEnv, "")
	m := buildModel(t)
	openJevSettings(t, m)
	m.settings.jevCursor = 1
	m.handleSettingsKey(key("enter"))
	m.Update(tea.PasteMsg{Content: testJevKey})
	if card := ansi.Strip(m.viewSettings()); strings.Contains(card, testJevKey) {
		t.Fatalf("key shown in clear while pasting:\n%s", card)
	}
	m.handleSettingsKey(key("esc"))
	if m.settings.jevPasting || m.jevSavedKey != "" {
		t.Fatal("esc saved the key")
	}
	if _, err := os.Stat(jevKeyPath(m.configDir())); !os.IsNotExist(err) {
		t.Fatalf("esc wrote a key file: %v", err)
	}
}

func TestMaskJevKey(t *testing.T) {
	for key, want := range map[string]string{
		"short-key":  "••••",
		testJevKey:   "••••WXYZ",
		"":           "••••",
		"abcdefghij": "••••",
	} {
		if got := maskJevKey(key); got != want {
			t.Fatalf("maskJevKey(%q) = %q want %q", key, got, want)
		}
	}
}

func TestJevCardToggleSharesTheExperiment(t *testing.T) {
	m := buildModel(t)
	openJevSettings(t, m)
	m.handleSettingsKey(key("right"))
	m.handleSettingsKey(key("esc"))
	m.handleSettingsKey(key("esc"))
	if !m.jevAutoSuggest || !storedJevAutoSuggest(m.store) {
		t.Fatal("JEV card toggle did not persist JEV Auto Suggest")
	}
	m.openSettings()
	m.settings.field = settingsFieldExperimental
	m.handleSettingsKey(key("enter"))
	if card := ansi.Strip(m.viewSettings()); !strings.Contains(card, "> JEV Auto Suggest  ◂ on ▸") {
		t.Fatalf("Experimental card disagrees with the JEV card:\n%s", card)
	}
}

func TestEveryExperimentTogglesAndPersists(t *testing.T) {
	m := buildModel(t)
	for i, feature := range experiments {
		if storedExperiment(m.store, feature.setting) {
			t.Fatalf("%s is on before it was turned on", feature.name)
		}
		m.openSettings()
		m.settings.field = settingsFieldExperimental
		m.handleSettingsKey(key("enter"))
		for range i {
			m.handleSettingsKey(key("down"))
		}
		if m.settings.experimentalCursor != i {
			t.Fatalf("cursor %d, want %d", m.settings.experimentalCursor, i)
		}
		m.handleSettingsKey(key("enter"))
		m.handleSettingsKey(key("esc"))
		m.handleSettingsKey(key("esc"))
		if !storedExperiment(m.store, feature.setting) {
			t.Fatalf("%s did not persist on", feature.name)
		}
	}
	m.openSettings()
	m.settings.field = settingsFieldExperimental
	m.handleSettingsKey(key("enter"))
	m.handleSettingsKey(key("up"))
	if m.settings.experimentalCursor != len(experiments)-1 {
		t.Fatalf("up from the first experiment landed on %d", m.settings.experimentalCursor)
	}
	m.handleSettingsKey(key("enter"))
	m.handleSettingsKey(key("esc"))
	m.handleSettingsKey(key("esc"))
	if storedExperiment(m.store, experiments[len(experiments)-1].setting) {
		t.Fatal("turning the last experiment off did not persist")
	}
}

func TestJevFeaturesStayOffWithASavedKeyUntilEnabled(t *testing.T) {
	t.Setenv(jevKeyEnv, "")
	m := newSessionJevModel(t)
	m.jevSavedKey = testJevKey
	if cmd := m.schedulePromptJev(); cmd != nil {
		t.Fatal("a saved key alone queued a JEV request with the experiment off")
	}
	m.jevAutoSuggest = true
	if cmd := m.schedulePromptJev(); cmd == nil {
		t.Fatal("a saved key with JEV on did not queue a request")
	}
	m.jevSavedKey = ""
	if cmd := m.schedulePromptJev(); cmd != nil {
		t.Fatal("no key at all still queued a request")
	}
}
