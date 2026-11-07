package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/git"
)

type worktreePicker struct {
	entries []git.Worktree
	index   int
	message string
}

type worktreesLoadedMsg struct {
	picker  *worktreePicker
	entries []git.Worktree
	dir     string
	message string
}

func (m *Model) openWorktreePicker() tea.Cmd {
	picker := &worktreePicker{message: "Looking for worktrees…"}
	m.form.worktrees = picker
	m.errBar.text = ""
	if m.gitDrv == nil {
		picker.message = "Git is not available. You can still enter a directory."
		return nil
	}
	driver, raw := m.gitDrv, m.form.dir.Value()
	return func() tea.Msg {
		result := worktreesLoadedMsg{picker: picker}
		cwd, _ := os.Getwd()
		dir, ok := resolveExistingDir(raw, cwd)
		if !ok {
			result.message = "Set the directory to an existing Git checkout first."
			return result
		}
		result.dir = dir
		entries, err := driver.Worktrees(dir)
		switch {
		case errors.Is(err, git.ErrNotARepo):
			result.message = "Set the directory to a Git checkout first."
		case err != nil:
			result.message = "Could not list worktrees: " + textfmt.FirstLine(err.Error(), 160)
		case len(entries) == 0:
			result.message = "No usable worktrees in this repository."
		default:
			result.entries = entries
		}
		return result
	}
}

func (m *Model) finishWorktreePicker(msg worktreesLoadedMsg) {
	picker := m.form.worktrees
	// A lookup may finish after Escape, or after a new picker has opened.
	if m.mode != modeForm || picker != msg.picker {
		return
	}
	picker.entries, picker.message = msg.entries, msg.message
	for i, entry := range picker.entries {
		if entry.Path == msg.dir {
			picker.index = i
			break
		}
	}
}

func (m *Model) handleWorktreePickerKey(msg tea.KeyPressMsg) {
	picker := m.form.worktrees
	switch msg.String() {
	case "esc":
		m.form.worktrees = nil
	case "up":
		picker.index = max(0, picker.index-1)
	case "down":
		if len(picker.entries) > 0 {
			picker.index = min(len(picker.entries)-1, picker.index+1)
		}
	case "enter":
		if len(picker.entries) > 0 {
			m.chooseWorktree(picker.entries[picker.index].Path)
		}
	}
}

func (m *Model) chooseWorktree(path string) {
	picker := m.form.worktrees
	if !isDir(path) {
		picker.message = "That checkout is no longer available."
		return
	}
	// The directory field is single-line and trimmed on submit. Refuse a
	// path it cannot represent rather than sending the session elsewhere.
	input := m.form.dir
	input.SetValue(path)
	if input.Value() != path || strings.TrimSpace(path) != path {
		picker.message = "That path cannot be entered in the directory field."
		return
	}
	m.setPathField(path)
	m.pathSugg.reset()
	m.form.worktrees = nil
}

func (m *Model) viewWorktreePicker() string {
	picker := m.form.worktrees
	const title = "◆ Choose a worktree"
	width := cardInnerWidth(m.cardWidth())
	hints := [][2]string{{"esc", "back to form"}}
	var body []string
	if picker.message != "" {
		message := cleanText(textfmt.OneLine(ansi.Strip(picker.message)))
		for _, line := range textfmt.Wrap(message, width) {
			body = append(body, mutedStyle.Render(line))
		}
	}
	if len(picker.entries) > 0 {
		hints = append([][2]string{{"↑↓", "pick"}, {"↵", "use directory"}}, hints...)
		wrappedHints := strings.Count(legendInline(hints, width), "\n")
		// Leave room for the frame, hints and the count below the two-line rows.
		room := m.height - 7 - wrappedHints - len(body)
		if m.errBar.text != "" {
			room -= 2
		}
		visible := max(1, room/2)
		start := min(max(0, picker.index-visible/2), max(0, len(picker.entries)-visible))
		end := min(start+visible, len(picker.entries))
		for i := start; i < end; i++ {
			entry := picker.entries[i]
			branch := entry.Branch
			if branch == "" {
				branch = "Detached HEAD"
				if entry.Head != "" {
					branch += " · " + entry.Head[:min(7, len(entry.Head))]
				}
			}
			marker, style := "  ", valueStyle
			if i == picker.index {
				marker, style = focusEdgeStyle.Render("❯ "), groupNameStyle
			}
			path := cleanText(entry.Path)
			if over := textfmt.Width(path) - (width - 2); over > 0 {
				path = ansi.TruncateLeft(path, over+1, "…")
			}
			body = append(body, marker+style.Render(textfmt.TruncateWidth(cleanText(branch), width-2, "…")),
				"  "+mutedStyle.Render(path))
		}
		if end-start < len(picker.entries) {
			body = append(body, mutedStyle.Render(fmt.Sprintf("%d–%d of %d", start+1, end, len(picker.entries))))
		}
	}
	return m.card(title, strings.Join(body, "\n"), hints)
}
