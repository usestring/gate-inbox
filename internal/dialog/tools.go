package dialog

import (
	"sync"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
)

type Reading struct {
	Questions []Question
	OnSubmit  bool
}

type ToolReader struct {
	Questions func(pane string, asked []convo.AskQuestion) (Reading, bool)
	Screen    func(pane string) (Screen, bool)
}

var (
	toolsMu sync.RWMutex
	tools   = map[string]ToolReader{}
)

func RegisterTool(tool string, reader ToolReader) {
	toolsMu.Lock()
	defer toolsMu.Unlock()
	tools[tool] = reader
}

func toolReader(tool string) (ToolReader, bool) {
	toolsMu.RLock()
	defer toolsMu.RUnlock()
	reader, ok := tools[tool]
	return reader, ok
}

func ReadQuestions(tool, pane string, asked []convo.AskQuestion) (Reading, bool) {
	if reader, ok := toolReader(tool); ok && reader.Questions != nil {
		return reader.Questions(pane, asked)
	}
	questions := Questions(pane, asked)
	if len(questions) == 0 {
		return Reading{}, false
	}
	stepper, _ := ParseStepper(pane)
	return Reading{Questions: questions, OnSubmit: stepper.OnSubmit()}, true
}

func ReadScreenFor(tool, pane string) (Screen, bool) {
	if reader, ok := toolReader(tool); ok && reader.Screen != nil {
		if screen, ok := reader.Screen(pane); ok {
			return screen, true
		}
	}
	return ReadScreen(ansi.Strip(pane))
}

func HasReader(tool string) bool {
	reader, ok := toolReader(tool)
	return ok && reader.Questions != nil
}
