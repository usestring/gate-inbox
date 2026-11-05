package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/tmux"
)

// The request contains operator input, never local credentials or account tokens.
type manualLaunchRequest struct {
	Tool                    string            `json:"tool"`
	Model                   string            `json:"model,omitempty"`
	Name                    string            `json:"name"`
	Directory               string            `json:"directory"`
	Prompt                  string            `json:"prompt,omitempty"`
	SkipPermissions         bool              `json:"skip_permissions,omitempty"`
	ChildrenSkipPermissions bool              `json:"children_skip_permissions,omitempty"`
	Form                    map[string]string `json:"form,omitempty"`
	Attachments             []string          `json:"attachments,omitempty"`
	MemoryAvailableBelow    float64           `json:"memory_available_below"`
}

type launchMemoryState struct {
	request   manualLaunchRequest
	local     func() (tea.Model, tea.Cmd)
	previous  mode
	available float64
	known     bool
	selected  int
}

type devboxLaunchDoneMsg struct {
	path string
	err  error
}

// Manual entry points own this choice; shared spawning and CLI/MCP never call it.
func (m *Model) confirmManualLaunch(request manualLaunchRequest, local func() (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	threshold := m.cfg.NewSession.MemoryAvailableBelow
	available := 100 - m.snap.MemPercent
	if threshold == 0 || (m.snap.MemOK && available >= threshold) {
		return local()
	}
	request.MemoryAvailableBelow = threshold
	m.launchMemory = launchMemoryState{
		request: request, local: local, previous: m.mode,
		available: available, known: m.snap.MemOK,
	}
	if strings.TrimSpace(m.cfg.NewSession.DevboxCommand) == "" {
		m.launchMemory.selected = 1
	}
	m.mode = modeLaunchMemory
	return m, nil
}

func (m *Model) handleLaunchMemoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.mode = m.launchMemory.previous
		m.launchMemory = launchMemoryState{}
		m.errBar.text = ""
	case "up", "k":
		m.launchMemory.selected = (m.launchMemory.selected + 2) % 3
	case "down", "j", "tab":
		m.launchMemory.selected = (m.launchMemory.selected + 1) % 3
	case "enter":
		switch m.launchMemory.selected {
		case 0:
			return m.launchOnDevbox()
		case 1:
			local := m.launchMemory.local
			m.mode = m.launchMemory.previous
			m.launchMemory = launchMemoryState{}
			m.errBar.text = ""
			return local()
		case 2:
			return m.handleLaunchMemoryKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		}
	}
	return m, nil
}

func (m *Model) launchOnDevbox() (tea.Model, tea.Cmd) {
	command := strings.TrimSpace(m.cfg.NewSession.DevboxCommand)
	if command == "" {
		m.errBar.text = "configure a devbox launcher first, or choose Launch locally anyway"
		return m, nil
	}
	body, err := json.Marshal(m.launchMemory.request)
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	file, err := os.CreateTemp("", "gate-inbox-launch-*.json")
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	path := file.Name()
	_, writeErr := file.Write(body)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		m.errBar.text = fmt.Sprintf("prepare devbox launch: %v", errors.Join(writeErr, closeErr))
		return m, nil
	}
	process := exec.Command("sh", "-c", command+" "+tmux.ShellQuote(path))
	return m, tea.ExecProcess(process, func(err error) tea.Msg {
		return devboxLaunchDoneMsg{path: path, err: err}
	})
}

func (m *Model) finishDevboxLaunch(msg devboxLaunchDoneMsg) (tea.Model, tea.Cmd) {
	_ = os.Remove(msg.path)
	if msg.err != nil {
		m.errBar.text = "Devbox launch failed: " + msg.err.Error() + ". Draft kept."
		return m, nil
	}
	m.launchMemory = launchMemoryState{}
	m.mode = modeList
	m.reportDone("devbox session opened")
	return m, m.refreshCmd()
}

func (m *Model) viewLaunchMemory() string {
	state := m.launchMemory
	message := fmt.Sprintf("Available memory: %.1f%%\nWarning threshold: %.1f%%\n\nChoose where to start this session.", state.available, state.request.MemoryAvailableBelow)
	if !state.known {
		message = "Memory availability could not be read.\nChoose where to start this session."
	}
	options := []string{"Launch on devbox (recommended)", "Launch locally anyway", "Cancel"}
	if strings.TrimSpace(m.cfg.NewSession.DevboxCommand) == "" {
		options[0] = "Launch on devbox (not configured)"
	}
	var body strings.Builder
	body.WriteString(mutedStyle.Render(message) + "\n\n")
	for i, option := range options {
		if i == state.selected {
			body.WriteString(annotationStyle.Render("› " + option))
		} else {
			body.WriteString(mutedStyle.Render("  " + option))
		}
		body.WriteByte('\n')
	}
	return m.cardSized(m.cardWidth(), "Memory before a new session", strings.TrimRight(body.String(), "\n"), [][2]string{{"↑/↓", "choose"}, {"enter", "continue"}, {"esc", "cancel"}})
}
