// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package git

import (
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func testRepo(t *testing.T) (*Driver, string) {
	t.Helper()
	driver, err := New()
	if err != nil {
		t.Skip("git not installed")
	}
	dir := tmuxtest.ScratchDir(t)
	gitIn(t, dir, "init", "-b", "main")
	gitIn(t, dir, "config", "user.email", "test@test")
	gitIn(t, dir, "config", "user.name", "test")
	return driver, dir
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func commit(t *testing.T, dir, message string) {
	t.Helper()
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", message)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsRepoRoot(t *testing.T) {
	driver, dir := testRepo(t)
	write(t, dir, "a.go", "package a\n")
	commit(t, dir, "init")
	if !driver.IsRepoRoot(dir) {
		t.Fatal("repo root should be recognised")
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if driver.IsRepoRoot(sub) {
		t.Fatal("a subdirectory is not the root")
	}
	if driver.IsRepoRoot(t.TempDir()) {
		t.Fatal("a non-repo dir is not a root")
	}
}

func TestRepoRoot(t *testing.T) {
	driver, dir := testRepo(t)
	write(t, dir, "a.txt", "x")
	commit(t, dir, "seed")
	root, err := driver.RepoRoot(dir)
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	if resolved, _ := filepath.EvalSymlinks(dir); root != resolved && root != dir {
		t.Fatalf("root = %q, want %q", root, dir)
	}
	if _, err := driver.RepoRoot(t.TempDir()); err == nil {
		t.Fatal("non-repo dir should error")
	}
}
