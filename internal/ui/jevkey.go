package ui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

const jevKeyEnv = "TYPESAFE_API_KEY"

// jevKeyFile is the pasted TypeSafe key. It sits beside the sealing keys so
// managed sessions are denied it the same way (see parentseal/keys.go); the
// dot keeps the name apart from the session ids that directory is keyed by.
const jevKeyFile = "typesafe.key"

type jevKeySource int

const (
	jevKeyNone jevKeySource = iota
	jevKeyFromEnv
	jevKeySaved
)

func jevKeyPath(configDir string) string {
	return filepath.Join(parentseal.KeyDir(configDir), jevKeyFile)
}

func readJevKey(configDir string) (string, error) {
	if configDir == "" {
		return "", nil
	}
	data, err := os.ReadFile(jevKeyPath(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return strings.TrimSpace(string(data)), err
}

func writeJevKey(configDir, key string) error {
	if configDir == "" {
		return errors.New("no config directory to save the key in")
	}
	dir := parentseal.KeyDir(configDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".typesafe-*")
	if err != nil {
		return err
	}
	if _, err := file.WriteString(key); err != nil {
		file.Close()
		os.Remove(file.Name())
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return err
	}
	return os.Rename(file.Name(), jevKeyPath(configDir))
}

func removeJevKey(configDir string) error {
	if configDir == "" {
		return nil
	}
	err := os.Remove(jevKeyPath(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// jevKey is the key JEV requests use. TYPESAFE_API_KEY wins over a saved
// key, the way every other credential here reads the environment first.
func (m *Model) jevKey() (string, jevKeySource) {
	if key := strings.TrimSpace(os.Getenv(jevKeyEnv)); key != "" {
		return key, jevKeyFromEnv
	}
	if m.jevSavedKey != "" {
		return m.jevSavedKey, jevKeySaved
	}
	return "", jevKeyNone
}

func (m *Model) jevAPIKey() string {
	key, _ := m.jevKey()
	return key
}

// maskJevKey shows the last four characters of a key long enough that four
// give little away, and none of a shorter one.
func maskJevKey(key string) string {
	runes := []rune(key)
	if len(runes) < 12 {
		return "••••"
	}
	return "••••" + string(runes[len(runes)-4:])
}

func (m *Model) jevAccess() (string, bool) {
	key, source := m.jevKey()
	switch source {
	case jevKeyFromEnv:
		return "access: key from " + jevKeyEnv + " " + maskJevKey(key), true
	case jevKeySaved:
		return "access: saved key " + maskJevKey(key), true
	}
	return "no access: paste a key or set " + jevKeyEnv, false
}

func (m *Model) jevSettingsSummary() string {
	state := "off"
	if m.settings.jevAutoSuggest {
		state = "on"
	}
	switch _, source := m.jevKey(); source {
	case jevKeyFromEnv:
		return state + " · key from " + jevKeyEnv
	case jevKeySaved:
		return state + " · saved key"
	}
	return state + " · no key"
}

func (m *Model) loadJevKey() {
	key, err := readJevKey(m.configDir())
	if err != nil {
		m.errBar.text = "reading the saved JEV key: " + err.Error()
	}
	m.jevSavedKey = key
}

func (m *Model) handleJevSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settings.jevPasting {
		switch msg.String() {
		case "enter":
			m.saveJevKey(m.settings.jevInput.Value())
		case "esc":
			m.settings.jevPasting = false
		default:
			var cmd tea.Cmd
			m.settings.jevInput, cmd = m.settings.jevInput.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	switch msg.String() {
	case "up", "k", "down", "j":
		m.settings.jevCursor = 1 - m.settings.jevCursor
	case "left", "right", "h", "l", "space":
		if m.settings.jevCursor == 0 {
			m.settings.jevAutoSuggest = !m.settings.jevAutoSuggest
		}
	case "enter":
		if m.settings.jevCursor == 0 {
			m.settings.jevAutoSuggest = !m.settings.jevAutoSuggest
			return m, nil
		}
		input := textinput.New()
		input.Prompt = ""
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '•'
		input.CharLimit = 512
		input.Focus()
		m.settings.jevInput = input
		m.settings.jevPasting = true
	case "x", "delete", "backspace":
		if m.settings.jevCursor == 1 && m.jevSavedKey != "" {
			if err := removeJevKey(m.configDir()); err != nil {
				m.errBar.text = "removing the saved JEV key: " + err.Error()
				return m, nil
			}
			m.jevSavedKey = ""
		}
	case "esc":
		m.settings.jevPanel = false
	}
	return m, nil
}

func (m *Model) saveJevKey(value string) {
	key := strings.Join(strings.Fields(value), "")
	if key == "" {
		m.errBar.text = "paste a key, or esc to keep the current one"
		return
	}
	if err := writeJevKey(m.configDir(), key); err != nil {
		m.errBar.text = "saving the JEV key: " + err.Error()
		return
	}
	m.jevSavedKey = key
	m.settings.jevPasting = false
	m.settings.jevInput = textinput.Model{}
	m.errBar.text = ""
}

func (m *Model) pasteIntoJevKey(msg tea.PasteMsg) tea.Cmd {
	var cmd tea.Cmd
	m.settings.jevInput, cmd = m.settings.jevInput.Update(msg)
	return cmd
}

func (m *Model) viewJevSettings() string {
	marker := func(row int) string {
		if m.settings.jevCursor == row {
			return lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
		}
		return "  "
	}
	state := "off"
	if m.settings.jevAutoSuggest {
		state = "on"
	}
	var body strings.Builder
	body.WriteString(marker(0) + padRight(valueStyle.Render("JEV Auto Suggest"), settingsLabelColumn) +
		subtleStyle.Render("◂ ") + valueStyle.Render(state) + subtleStyle.Render(" ▸") + "\n")
	body.WriteString(marker(1) + padRight(valueStyle.Render("API key"), settingsLabelColumn))
	hint := [][2]string{{"↑↓", "field"}, {"←→/↵", "toggle"}, {"esc", "back"}}
	switch {
	case m.settings.jevPasting:
		body.WriteString(m.settings.jevInput.View())
		hint = [][2]string{{"↵", "save key"}, {"esc", "cancel"}}
	case m.jevSavedKey != "":
		body.WriteString(mutedStyle.Render("saved " + maskJevKey(m.jevSavedKey)))
		if m.settings.jevCursor == 1 {
			hint = [][2]string{{"↑↓", "field"}, {"↵", "replace key"}, {"x", "remove key"}, {"esc", "back"}}
		}
	default:
		body.WriteString(keyStyle.Render("↵") + mutedStyle.Render(" paste a key"))
		if m.settings.jevCursor == 1 {
			hint = [][2]string{{"↑↓", "field"}, {"↵", "paste key"}, {"esc", "back"}}
		}
	}
	access, ok := m.jevAccess()
	if ok {
		access = valueStyle.Render("✓ " + access)
	} else {
		access = mutedStyle.Render("✗ " + access)
	}
	body.WriteString("\n\n" + access + "\n\n" + mutedStyle.Render(
		"Suggests replies and ranks New Session prompts through TypeSafe.\n"+
			jevKeyEnv+" wins over a saved key; the saved key is kept\n"+
			"0600 beside Gate Inbox's channel keys, which sessions cannot read."))
	return m.cardFlex("▣ JEV", body.String(), hint)
}
