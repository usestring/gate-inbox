package ui

import (
	"errors"
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
	err   error
}
type promptSnipsTickMsg struct{}

var promptHistoryRoots = newHistoryLocator

func loadPromptSnips() tea.Msg {
	roots := promptHistoryRoots()
	if roots.ClaudeHome == "" || roots.CodexRoot == "" {
		return promptSnipsLoadedMsg{err: errors.New("cannot locate prompt history")}
	}
	subs, err := promptsnips.ReadHistory(roots.ClaudeHome, filepath.Dir(roots.CodexRoot), time.Now().Add(-promptsnips.Window))
	if err != nil {
		return promptSnipsLoadedMsg{err: err}
	}
	return promptSnipsLoadedMsg{snips: promptsnips.Build(subs, time.Now())}
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
