package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreesListsUsableCheckouts(t *testing.T) {
	driver, repo := testRepo(t)
	write(t, repo, "README", "seed\n")
	commit(t, repo, "seed")

	dir := t.TempDir()
	linked := filepath.Join(dir, "release checkout")
	detached := filepath.Join(dir, "detached")
	missing := filepath.Join(dir, "offline")
	gitIn(t, repo, "worktree", "add", "-b", "feature/release", linked)
	gitIn(t, repo, "worktree", "add", "--detach", detached)
	gitIn(t, repo, "worktree", "add", "--detach", missing)
	gitIn(t, repo, "worktree", "lock", missing)
	if err := os.Rename(missing, filepath.Join(dir, "offline-moved")); err != nil {
		t.Fatal(err)
	}

	got, err := driver.Worktrees(linked)
	if err != nil || len(got) != 3 {
		t.Fatalf("worktrees = %+v, err = %v; want main, linked and detached checkouts", got, err)
	}
	want := map[string]string{repo: "main", linked: "feature/release", detached: ""}
	for path, branch := range want {
		expected, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tree := range got {
			info, err := os.Stat(tree.Path)
			if err == nil && os.SameFile(info, expected) {
				if tree.Branch != branch || tree.Head == "" {
					t.Errorf("worktree %+v, want branch %q and a commit", tree, branch)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("checkout %q missing from %+v", path, got)
		}
	}
}
