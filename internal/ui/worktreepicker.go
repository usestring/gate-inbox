package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/git"
)

type worktreePicker struct {
	entries []git.Worktree
	filter  textinput.Model
	current int
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
	filter := textField("Filter by branch or path", 400)
	filter.Focus()
	picker := &worktreePicker{filter: filter, current: -1, message: "Looking for worktrees…"}
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
		entries, err := driver.Worktrees(dir)
		switch {
		case errors.Is(err, git.ErrNotARepo):
			result.message = "Set the directory to a Git checkout first."
		case err != nil:
			result.message = "Could not list worktrees: " + textfmt.FirstLine(err.Error(), 160)
		case len(entries) == 0:
			result.message = "No usable worktrees in this repository."
		default:
			if resolved, err := filepath.EvalSymlinks(dir); err == nil {
				dir = resolved
			}
			result.entries = entries
			result.dir = dir
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
	// A directory inside a checkout still belongs to it. Prefer the closest
	// checkout when a linked worktree is nested inside another one.
	for i, entry := range picker.entries {
		rel, err := filepath.Rel(entry.Path, msg.dir)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) &&
			(picker.current < 0 || len(entry.Path) > len(picker.entries[picker.current].Path)) {
			picker.current = i
		}
	}
	picker.index = max(0, picker.current)
}

func (p *worktreePicker) matches() []git.Worktree {
	query := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	if query == "" {
		return p.entries
	}
	var matches []git.Worktree
	for _, entry := range p.entries {
		if strings.Contains(strings.ToLower(entry.Branch), query) || strings.Contains(strings.ToLower(entry.Path), query) {
			matches = append(matches, entry)
		}
	}
	return matches
}

func (p *worktreePicker) updateFilter(msg tea.Msg) tea.Cmd {
	if len(p.entries) == 0 {
		return nil
	}
	before := p.filter.Value()
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(msg)
	if value := p.filter.Value(); value != before {
		p.index = 0
		if strings.TrimSpace(value) == "" {
			p.index = max(0, p.current)
		}
		p.message = ""
	}
	return cmd
}

func (m *Model) handleWorktreePickerKey(msg tea.KeyPressMsg) tea.Cmd {
	picker := m.form.worktrees
	matches := picker.matches()
	switch msg.String() {
	case "esc":
		m.form.worktrees = nil
	case "up", "ctrl+p", "shift+tab":
		picker.index = max(0, picker.index-1)
	case "down", "ctrl+n", "tab":
		if len(matches) > 0 {
			picker.index = min(len(matches)-1, picker.index+1)
		}
	case "enter":
		if len(matches) > 0 {
			m.chooseWorktree(matches[picker.index].Path)
		}
	default:
		return picker.updateFilter(msg)
	}
	return nil
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
	cardWidth := m.cardWidth()
	if m.width >= 28 {
		cardWidth = min(88, m.width-4)
	}
	width := cardInnerWidth(cardWidth)
	hints := [][2]string{{"esc", "back"}}
	var body []string
	if len(picker.entries) > 0 {
		picker.filter.SetWidth(width - 3)
		picker.filter.SetCursor(picker.filter.Position())
		body = append(body, picker.filter.View())
	}
	if picker.message != "" {
		message := cleanText(textfmt.OneLine(ansi.Strip(picker.message)))
		for _, line := range textfmt.Wrap(message, width) {
			body = append(body, mutedStyle.Render(line))
		}
	}
	matches := picker.matches()
	if len(matches) == 0 && len(picker.entries) > 0 {
		body = append(body, mutedStyle.Render("No worktrees match."))
	}
	if len(matches) > 0 {
		hints = append([][2]string{{"↑↓", "pick"}, {"↵", "use directory"}}, hints...)
		wrappedHints := strings.Count(legendInline(hints, width), "\n")
		// Leave room for the frame, hints and the count below the two-line rows.
		room := m.height - 7 - wrappedHints - len(body)
		if m.errBar.text != "" {
			room -= 2
		}
		visible := max(1, room/2)
		start := min(max(0, picker.index-visible/2), max(0, len(matches)-visible))
		end := min(start+visible, len(matches))
		for i := start; i < end; i++ {
			entry := matches[i]
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
			suffix := ""
			if picker.current >= 0 && entry.Path == picker.entries[picker.current].Path {
				suffix = " · current"
			}
			path := cleanText(entry.Path)
			if over := textfmt.Width(path) - (width - 2); over > 0 {
				path = ansi.TruncateLeft(path, over+1, "…")
			}
			branch = textfmt.TruncateWidth(cleanText(branch), width-2-textfmt.Width(suffix), "…")
			body = append(body, marker+style.Render(branch)+mutedStyle.Render(suffix),
				"  "+mutedStyle.Render(path))
		}
		if end-start < len(matches) {
			body = append(body, mutedStyle.Render(fmt.Sprintf("%d–%d of %d", start+1, end, len(matches))))
		}
	}
	return m.cardSized(cardWidth, title, strings.Join(body, "\n"), hints)
}
