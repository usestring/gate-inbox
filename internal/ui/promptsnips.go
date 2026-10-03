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
	seq   int
	snips []promptsnips.Snippet
	err   error
}
type promptSnipsTickMsg struct{ seq int }

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

func (m *Model) refreshPromptSnips() tea.Cmd {
	if !m.promptSuggest {
		return nil
	}
	seq := m.promptSnipsSeq
	return func() tea.Msg {
		msg := loadPromptSnips().(promptSnipsLoadedMsg)
		msg.seq = seq
		return msg
	}
}

func promptSnipsTick(seq int) tea.Cmd {
	return tea.Tick(promptSnipsRefreshInterval, func(time.Time) tea.Msg { return promptSnipsTickMsg{seq: seq} })
}

func (m *Model) promptSuggestionLine(c *composer, width int) string {
	if !m.promptSuggest {
		return ""
	}
	choice := m.promptJevChoice()
	suggestions := c.suggestions(m.promptSnips, choice)
	if len(suggestions) == 0 {
		return ""
	}
	index := c.suggestionIndex % len(suggestions)
	label := "^N/P ^Y " + strconv.Itoa(index+1) + "/" + strconv.Itoa(len(suggestions)) + "  "
	if choice != "" && suggestions[index].Key == choice {
		label += "jev  "
	}
	line := label + strings.Join(strings.Fields(suggestions[index].Text), " ")
	return subtleStyle.Render(textfmt.TruncateWidth(line, width, "…"))
}
