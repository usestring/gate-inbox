package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/git"
	"github.com/usestring/gate-inbox/internal/logging"
)

const recentWorktreePrefix = "recent_worktree:"

type recentWorktreeMsg struct {
	formGen int
	dir     string
	repo    string
	tree    git.Worktree
}

// Remember the checkout root rather than the launch directory, which can be
// a subdirectory. The common Git directory keeps separate clones apart.
func (m *Model) rememberRecentWorktree(dir string) {
	if m.gitDrv == nil {
		return
	}
	repo, root, err := m.gitDrv.WorktreeLocation(dir)
	if err != nil {
		return
	}
	if err := m.store.SetSetting(recentWorktreePrefix+repo, root); err != nil {
		logging.Warn("save recent worktree", logging.Err(err))
	}
}

func (m *Model) lookupRecentWorktree() tea.Cmd {
	m.form.recent = recentWorktreeMsg{}
	driver, st := m.gitDrv, m.store
	if driver == nil {
		return nil
	}
	formGen, raw := m.form.prompt.gen, m.form.dir.Value()
	return func() tea.Msg {
		result := recentWorktreeMsg{formGen: formGen, dir: raw}
		dir, ok := resolveExistingDir(raw, "")
		if !ok {
			return result
		}
		repo, root, err := driver.WorktreeLocation(dir)
		if err != nil {
			return result
		}
		path, err := st.Setting(recentWorktreePrefix + repo)
		if err != nil {
			logging.Warn("load recent worktree", logging.Err(err))
			return result
		}
		if path == "" || path == root {
			return result
		}
		entries, err := driver.Worktrees(dir)
		if err != nil {
			return result
		}
		for _, tree := range entries {
			if tree.Path == path {
				result.repo, result.tree = repo, tree
				break
			}
		}
		return result
	}
}

func (m *Model) finishRecentWorktree(msg recentWorktreeMsg) {
	if m.mode == modeForm && m.form.dirAuto && m.form.prompt.gen == msg.formGen && m.form.dir.Value() == msg.dir {
		m.form.recent = msg
	}
}

func (m *Model) hasRecentWorktree() bool {
	return m.form.dirAuto && m.form.recent.tree.Path != ""
}

func (m *Model) useRecentWorktree() {
	recent := m.form.recent
	m.form.recent = recentWorktreeMsg{}
	// The checkout may have been removed or replaced while the form was open.
	path := recent.tree.Path
	repo, root, err := m.gitDrv.WorktreeLocation(path)
	if err != nil || repo != recent.repo || root != path {
		m.errBar.text = "That checkout is no longer available."
		return
	}
	m.errBar.text = m.useWorktreePath(path)
}

func (m *Model) viewRecentWorktree() string {
	tree := m.form.recent.tree
	branch := cleanText(tree.Branch)
	if branch == "" {
		branch = "Detached HEAD"
	}
	path := cleanText(tree.Path)
	const separator = " · "
	width := m.formValueWidth() - textfmt.Width(separator)
	branch = textfmt.TruncateWidth(branch, width/2, "…")
	if over := textfmt.Width(path) - (width - textfmt.Width(branch)); over > 0 {
		path = ansi.TruncateLeft(path, over+1, "…")
	}
	return valueStyle.Render(branch) + mutedStyle.Render(separator+path)
}
