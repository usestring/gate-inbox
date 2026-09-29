package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/promptsnips"
)

const promptSnipsRefreshInterval = time.Minute

type promptSnipsLoadedMsg struct {
	snips []promptsnips.Snippet
	ok    bool
}
type promptSnipsTickMsg struct{}

var readPromptHistory = promptsnips.ReadHistory

func loadPromptSnips() tea.Msg {
	home, err := os.UserHomeDir()
	if err != nil {
		return promptSnipsLoadedMsg{}
	}
	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	subs, err := readPromptHistory(claudeHome, codexHome, time.Now().Add(-promptsnips.Window))
	if err != nil {
		return promptSnipsLoadedMsg{}
	}
	return promptSnipsLoadedMsg{snips: promptsnips.Build(subs, time.Now()), ok: true}
}

func promptSnipsTick() tea.Cmd {
	return tea.Tick(promptSnipsRefreshInterval, func(time.Time) tea.Msg { return promptSnipsTickMsg{} })
}

func (m *Model) promptSuggestionLine(c *composer, width int) string {
	suggestions := c.suggestions(m.promptSnips)
	if len(suggestions) == 0 {
		return ""
	}
	index := c.suggestionIndex % len(suggestions)
	label := "^N/P ^Y " + strconv.Itoa(index+1) + "/" + strconv.Itoa(len(suggestions)) + "  "
	line := label + strings.Join(strings.Fields(suggestions[index].Text), " ")
	return subtleStyle.Render(textfmt.TruncateWidth(line, width, "…"))
}
