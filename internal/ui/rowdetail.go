package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/usestring/gate-inbox/internal/store"
)

// A session row spells its state and its CLI out beside the age:
// "working · claude · 2h ago". The words are for somebody still learning the
// board. Once the marks are learned, the tinted mark ahead of the name already
// says the state, and the words only take room from the name.
//
// The marks mode drops the state word and writes each CLI as two letters in
// that CLI's own colour. The letters are a stand-in for the CLIs' logos: no
// font a phone or an iPad falls back on has those logos, and a mark the
// client cannot draw comes out as a `?`. A group's rollup reads in marks too,
// "◐ 2  ◆ 1" rather than "2 working · 1 waiting".

const (
	rowDetailSetting = "row_detail"
	rowDetailWords   = "words"
	rowDetailMarks   = "marks"
)

// rowDetailModes is the setting's cycle order.
var rowDetailModes = []string{rowDetailWords, rowDetailMarks}

func storedRowDetail(st *store.Store) string {
	chosen, err := st.Setting(rowDetailSetting)
	if err != nil {
		return rowDetailWords
	}
	return normalizeRowDetail(chosen)
}

func normalizeRowDetail(chosen string) string {
	for _, mode := range rowDetailModes {
		if chosen == mode {
			return mode
		}
	}
	return rowDetailWords
}

func (m *Model) rowMarks() bool { return m.rowDetail == rowDetailMarks }

// toolMark is a CLI's two letters and the colour they are drawn in. The
// colours follow each vendor's own: Claude's clay orange, OpenAI's green for
// Codex, and OpenCode's blue. A CLI not listed keeps its name.
type toolMark struct {
	letters string
	color   string
}

var toolMarks = map[string]toolMark{
	"claude":   {letters: "CC", color: "#d97757"},
	"codex":    {letters: "CX", color: "#10a37f"},
	"opencode": {letters: "OC", color: "#4f8ff7"},
}

// toolLabel is how a row in marks mode names its CLI. Under the quiet
// palette the letters take the meta tint like the rest of the line: that
// palette keeps colour for the states that need a person, and a CLI is not
// one of them.
func (m *Model) toolLabel(tool string, metaText func(string) string) string {
	mark, ok := toolMarks[tool]
	if !ok {
		return metaText(tool)
	}
	if quietPalette {
		return metaText(mark.letters)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(mark.color)).Render(mark.letters)
}
