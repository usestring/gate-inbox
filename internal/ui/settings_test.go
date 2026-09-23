// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/snippets"
)

func TestDefaultToolFallsBackWhenSettingStale(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting("default_tool", "deleted-tool"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	if got := m.defaultTool(); got != "claude" {
		t.Fatalf("defaultTool = %q want claude (alphabetical fallback)", got)
	}
}

func TestSettingsTogglesQuickClose(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	if m.settings.quickCloseSend {
		t.Fatal("settings should open on stay-open by default")
	}
	for i := 0; i < settingsFieldQuickClose; i++ {
		m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.settings.field != settingsFieldQuickClose {
		t.Fatalf("stepping down should reach the quick send field, got %d", m.settings.field)
	}
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyRight})
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.quickCloseAfterSend() {
		t.Fatal("close choice should persist after toggle")
	}
}

func TestSettingsShowsVersion(t *testing.T) {
	m := &Model{
		version:  "v0.9.0",
		settings: settingsState{toolNames: []string{"claude"}},
	}
	out := m.viewSettings()
	if !strings.Contains(out, "version") || !strings.Contains(out, "v0.9.0") {
		t.Errorf("settings missing version: %q", out)
	}
	m.settings.field = settingsFieldVersion
	out = m.viewSettings()
	// Plain text, not the exact escape bytes: embedding a fastStyle render
	// inside a painted row rewrites its own inner reset to keep the row's
	// fill going, so the keyCap's standalone bytes no longer appear verbatim.
	if !strings.Contains(ansi.Strip(out), ansi.Strip(keyCap("↵/esc", "save"))) {
		t.Errorf("the version row should hint save, it carries no action: %q", out)
	}
	if strings.Contains(ansi.Strip(out), "↵ update") {
		t.Errorf("the version row must offer no update action: %q", out)
	}
}

func TestSettingsCLIPickerHidesFromNewSessions(t *testing.T) {
	m := buildModel(t)
	m.cfg = config.Config{Tools: map[string]config.Tool{
		"claude": {Command: "cat"},
		"codex":  {Command: "cat"},
		"grok":   {Command: "cat"},
	}}
	m.openSettings()
	m.settings.field = settingsFieldCLIs
	m.handleSettingsKey(key("enter"))
	if !m.settings.cliPicker {
		t.Fatal("enter on CLIs should open the picker")
	}
	out := m.viewSettings()
	for _, name := range []string{"claude", "codex", "grok"} {
		if !strings.Contains(out, name) {
			t.Fatalf("picker missing %q: %s", name, out)
		}
	}
	// Hide codex (index 1 in display order: claude, codex, grok).
	m.settings.cliCursor = 1
	m.handleSettingsKey(key(" "))
	if !m.settings.cliHidden["codex"] {
		t.Fatal("space should hide the focused CLI")
	}
	m.handleSettingsKey(key("esc"))
	if m.settings.cliPicker {
		t.Fatal("esc should leave the picker")
	}
	raw, err := m.store.Setting(hiddenToolsSetting)
	if err != nil || raw != "codex" {
		t.Fatalf("stored hidden_tools = %q err %v, want codex", raw, err)
	}

	enabled := m.enabledToolNames()
	for _, name := range enabled {
		if name == "codex" {
			t.Fatalf("codex should be omitted from create pickers: %v", enabled)
		}
	}
	m.openForm()
	if m.mode != modeForm {
		t.Fatalf("form should open with remaining CLIs, mode=%v err=%q", m.mode, m.errBar.text)
	}
	for _, name := range m.form.toolNames {
		if name == "codex" {
			t.Fatal("new-session form must not list a hidden CLI")
		}
	}
}

func TestSettingsCLIPickerKeepsOneEnabled(t *testing.T) {
	m := buildModel(t)
	m.cfg = config.Config{Tools: map[string]config.Tool{
		"claude": {Command: "cat"},
		"codex":  {Command: "cat"},
	}}
	m.openSettings()
	m.openCLIPicker()
	m.settings.cliCursor = 0
	m.handleSettingsKey(key("enter"))
	if !m.settings.cliHidden["claude"] {
		t.Fatal("first hide should succeed")
	}
	// Only codex left; refusing further hides.
	m.settings.cliCursor = 1
	m.handleSettingsKey(key("enter"))
	if m.settings.cliHidden["codex"] {
		t.Fatal("must not hide the last enabled CLI")
	}
	if !strings.Contains(m.errBar.text, "at least one") {
		t.Fatalf("want keep-one message, got %q", m.errBar.text)
	}
}

func TestSettingsOpensSnippetsInTheConfiguredEditor(t *testing.T) {
	m := buildModel(t)
	launched := captureEditor(t, "code")
	m.openSettings()
	m.settings.field = settingsFieldSnippets

	updated, cmd := m.handleSettingsKey(key("enter"))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatalf("snippet row returned no editor command: %s", m.errBar.text)
	}
	m.applyCmd(t, cmd)

	want := []string{"code", snippets.Path(m.configDir())}
	if !slices.Equal(*launched, want) {
		t.Fatalf("launched %v, want %v", *launched, want)
	}
	if m.mode != modeSettings {
		t.Fatalf("opening snippets left settings for mode %v", m.mode)
	}
	card := ansi.Strip(m.viewSettings())
	if !strings.Contains(card, "snippets") || !strings.Contains(card, "edit quick replies") {
		t.Fatalf("settings does not describe the snippets action: %s", card)
	}
}

// Snippets are edited in a file, so an operator with no editor configured
// had no way into the feature at all. A stock Unix box has vi, and that is
// enough to open one.
func TestSettingsOpensSnippetsWithNoEditorConfigured(t *testing.T) {
	m := buildModel(t)
	launched := captureEditor(t, "vi")
	m.openSettings()
	m.settings.field = settingsFieldSnippets

	updated, cmd := m.handleSettingsKey(key("enter"))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatalf("snippets should still open without a configured editor: %s", m.errBar.text)
	}
	// vi draws in this terminal, so it takes the screen through ExecProcess
	// rather than being started detached.
	if len(*launched) != 0 {
		t.Fatalf("vi must not start detached, got %v", *launched)
	}
	if strings.Contains(m.errBar.text, "config.toml") {
		t.Fatalf("no setting is needed to edit snippets, got %q", m.errBar.text)
	}
}

func TestSettingsReloadsEditedSnippetsWhenItCloses(t *testing.T) {
	m := buildModel(t)
	edited := []snippets.Snippet{{Key: "d", Label: "deploy", Text: "ship it now"}}
	encoded, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	m.openSettings()
	if err := os.WriteFile(snippets.Path(m.configDir()), encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	updated, _ := m.handleSettingsKey(key("esc"))
	m = updated.(*Model)
	loaded, ok := m.snippetFor("ctrl+alt+d")
	if !ok || loaded.Text != "ship it now" {
		t.Fatalf("edited snippet was not reloaded: %+v, found=%v", loaded, ok)
	}
}

func TestSettingsReportsAnInvalidSnippetEditWhenItCloses(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	if err := os.WriteFile(snippets.Path(m.configDir()), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	updated, _ := m.handleSettingsKey(key("esc"))
	m = updated.(*Model)
	if m.snipErr == "" || !strings.Contains(m.errBar.text, "snippets could not be read") {
		t.Fatalf("invalid edit was not reported: snipErr=%q errBar=%q", m.snipErr, m.errBar.text)
	}
}

func TestParseFormatHiddenTools(t *testing.T) {
	if got := parseHiddenTools(""); got != nil {
		t.Fatalf("empty parse = %v", got)
	}
	got := parseHiddenTools("codex, grok")
	if !got["codex"] || !got["grok"] || len(got) != 2 {
		t.Fatalf("parse = %v", got)
	}
	if formatHiddenTools(map[string]bool{"grok": true, "codex": true}) != "codex,grok" {
		t.Fatalf("format should sort: %q", formatHiddenTools(map[string]bool{"grok": true, "codex": true}))
	}
}

func TestSettingsHasNoTerminalRows(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	if strings.Contains(ansi.Strip(m.viewSettings()), "terminal rows") {
		t.Fatal("terminal rows setting must be gone")
	}
}
