package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorktreeLocation returns the Git directory shared by all linked checkouts
// and the root of the checkout containing dir.
func (d *Driver) WorktreeLocation(dir string) (commonDir, root string, err error) {
	out, err := d.run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	commonDir, root, ok := strings.Cut(out, "\n")
	if !ok || strings.Contains(root, "\n") || !filepath.IsAbs(commonDir) || !filepath.IsAbs(root) {
		return "", "", fmt.Errorf("git did not return a worktree location")
	}
	return filepath.Clean(commonDir), filepath.Clean(root), nil
}

// Worktree describes an existing checkout. Branch is empty on a detached HEAD.
type Worktree struct {
	Path   string
	Branch string
	Head   string
}

// Worktrees lists the usable checkouts of the repository containing dir.
func (d *Driver) Worktrees(dir string) ([]Worktree, error) {
	out, err := d.run(dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var worktrees []Worktree
	for record := range strings.SplitSeq(out, "\x00\x00") {
		var tree Worktree
		skip := false
		for field := range strings.SplitSeq(record, "\x00") {
			key, value, _ := strings.Cut(field, " ")
			switch key {
			case "worktree":
				tree.Path = value
			case "branch":
				tree.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "HEAD":
				tree.Head = value
			case "bare", "prunable":
				skip = true
			}
		}
		if skip || !filepath.IsAbs(tree.Path) {
			continue
		}
		// A locked worktree can be on a disconnected drive without being
		// marked prunable.
		if info, err := os.Stat(tree.Path); err == nil && info.IsDir() {
			tree.Path = filepath.Clean(tree.Path)
			worktrees = append(worktrees, tree)
		}
	}
	return worktrees, nil
}
