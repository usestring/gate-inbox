package ui

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/internal/classifier"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

const jevKeyEnv = "TYPESAFE_API_KEY"

type jevKeySource int

const (
	jevKeyNone jevKeySource = iota
	jevKeyFromEnv
	jevKeySaved
)

func jevKeyPath(configDir string) string { return classifier.JEV.KeyPath(configDir) }

func readJevKey(configDir string) (string, error) { return classifier.JEV.ReadSavedKey(configDir) }

func writeClassifierKey(provider classifier.Provider, configDir, key string) error {
	if provider != classifier.JEV && provider != classifier.Clef {
		return errors.New("unknown classifier provider")
	}
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
	file, err := os.CreateTemp(dir, ".classifier-*")
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
	return os.Rename(file.Name(), provider.KeyPath(configDir))
}

func removeClassifierKey(provider classifier.Provider, configDir string) error {
	if provider != classifier.JEV && provider != classifier.Clef {
		return errors.New("unknown classifier provider")
	}
	if configDir == "" {
		return nil
	}
	err := os.Remove(provider.KeyPath(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (m *Model) classifierProvider() classifier.Provider { return m.classifier }

func (m *Model) classifierSavedKey() *string {
	if m.classifierProvider() == classifier.Clef {
		return &m.clefSavedKey
	}
	return &m.jevSavedKey
}

func (m *Model) jevKey() (string, jevKeySource) {
	if key := strings.TrimSpace(os.Getenv(m.classifierProvider().KeyEnv())); key != "" {
		return key, jevKeyFromEnv
	}
	if key := *m.classifierSavedKey(); key != "" {
		return key, jevKeySaved
	}
	return "", jevKeyNone
}

func (m *Model) jevAPIKey() string {
	if m.classifierProvider().Endpoint() == "" {
		return ""
	}
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
	if m.classifier != classifier.JEV && m.classifier != classifier.Clef {
		return "no access: choose JEV or Clef", false
	}
	if m.classifierProvider().Endpoint() == "" {
		return "no access: set WORKERS_AI_URL to an HTTPS Worker origin", false
	}
	key, source := m.jevKey()
	switch source {
	case jevKeyFromEnv:
		return "access: key from " + m.classifierProvider().KeyEnv() + " " + maskJevKey(key), true
	case jevKeySaved:
		return "access: saved key " + maskJevKey(key), true
	}
	return "no access: paste a key or set " + m.classifierProvider().KeyEnv(), false
}

func (m *Model) jevSettingsSummary() string {
	state := "off"
	if m.settings.jevAutoSuggest {
		state = "on"
	}
	switch _, source := m.jevKey(); source {
	case jevKeyFromEnv:
		return state + " · key from " + m.classifierProvider().KeyEnv()
	case jevKeySaved:
		return state + " · saved key"
	}
	return state + " · no key"
}

func (m *Model) loadJevKey() {
	value, err := m.store.Setting(classifier.ProviderSetting)
	if err != nil {
		m.classifier = classifier.Provider("invalid")
		m.errBar.text = "reading classifier provider: " + err.Error()
	} else {
		m.classifier = classifier.Parse(value)
	}
	key, err := readJevKey(m.configDir())
	if err != nil {
		m.errBar.text = "reading the saved JEV key: " + err.Error()
	}
	m.jevSavedKey = key
	m.clefSavedKey, err = classifier.Clef.ReadSavedKey(m.configDir())
	if err != nil {
		m.errBar.text = "reading the saved Clef key: " + err.Error()
	}
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
	case "up", "k":
		m.settings.jevCursor = (m.settings.jevCursor + 2) % 3
	case "down", "j":
		m.settings.jevCursor = (m.settings.jevCursor + 1) % 3
	case "left", "right", "h", "l", "space":
		if m.settings.jevCursor == 2 {
			m.switchClassifier()
			return m, nil
		}
		if m.settings.jevCursor == 0 {
			m.settings.jevAutoSuggest = !m.settings.jevAutoSuggest
		}
	case "enter":
		if m.settings.jevCursor == 2 {
			m.switchClassifier()
			return m, nil
		}
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
		if m.settings.jevCursor == 1 && *m.classifierSavedKey() != "" {
			if err := removeClassifierKey(m.classifierProvider(), m.configDir()); err != nil {
				m.errBar.text = "removing the saved classifier key: " + err.Error()
				return m, nil
			}
			*m.classifierSavedKey() = ""
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
	if err := writeClassifierKey(m.classifierProvider(), m.configDir(), key); err != nil {
		m.errBar.text = "saving the classifier key: " + err.Error()
		return
	}
	*m.classifierSavedKey() = key
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
	body.WriteString(marker(0) + padRight(valueStyle.Render("Auto Suggest"), settingsLabelColumn) +
		subtleStyle.Render("◂ ") + valueStyle.Render(state) + subtleStyle.Render(" ▸") + "\n")
	body.WriteString(marker(1) + padRight(valueStyle.Render("API key"), settingsLabelColumn))
	hint := [][2]string{{"↑↓", "field"}, {"←→/↵", "toggle"}, {"esc", "back"}}
	switch {
	case m.settings.jevPasting:
		body.WriteString(m.settings.jevInput.View())
		hint = [][2]string{{"↵", "save key"}, {"esc", "cancel"}}
	case *m.classifierSavedKey() != "":
		body.WriteString(mutedStyle.Render("saved " + maskJevKey(*m.classifierSavedKey())))
		if m.settings.jevCursor == 1 {
			hint = [][2]string{{"↑↓", "field"}, {"↵", "replace key"}, {"x", "remove key"}, {"esc", "back"}}
		}
	default:
		body.WriteString(keyStyle.Render("↵") + mutedStyle.Render(" paste a key"))
		if m.settings.jevCursor == 1 {
			hint = [][2]string{{"↑↓", "field"}, {"↵", "paste key"}, {"esc", "back"}}
		}
	}
	body.WriteString("\n" + marker(2) + padRight(valueStyle.Render("Provider"), settingsLabelColumn) +
		subtleStyle.Render("◂ ") + valueStyle.Render(m.classifierProvider().Name()) + subtleStyle.Render(" ▸"))
	access, ok := m.jevAccess()
	if ok {
		access = valueStyle.Render("✓ " + access)
	} else {
		access = mutedStyle.Render("✗ " + access)
	}
	body.WriteString("\n\n" + access + "\n\n" + mutedStyle.Render(
		"Choose JEV (TypeSafe) or Clef (Workers AI).\n"+
			"Clef also needs WORKERS_AI_URL set to a trusted HTTPS Worker origin.\n"+
			m.classifierProvider().KeyEnv()+" wins over a saved key; the saved key is kept\n"+
			"0600 beside Gate Inbox's channel keys, which sessions cannot read."))
	return m.cardFlex("▣ Classifier", body.String(), hint)
}

func (m *Model) switchClassifier() {
	provider := classifier.Clef
	if m.classifierProvider() == classifier.Clef {
		provider = classifier.JEV
	}
	if err := m.store.SetSetting(classifier.ProviderSetting, string(provider)); err != nil {
		m.errBar.text = "saving classifier provider: " + err.Error()
		return
	}
	m.classifier = provider
	// In-flight results and cached picks belong to the previous provider.
	m.autoSuggestSeq++
	m.quick.suggestions = nil
	m.quick.suggestionIdentity = ""
	m.promptJev = promptJevState{seq: m.promptJev.seq + 1}
	m.jevCopy = jevCopyState{generation: m.jevCopy.generation + 1, busy: m.jevCopy.busy, copying: m.jevCopy.copying}
	m.jevFinish.generation++
	m.jevFinish.checked = nil
	m.jevFinish.asked = nil
}
