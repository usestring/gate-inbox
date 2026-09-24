package gitroot_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension/gitroot"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test",
		"-c", "protocol.file.allow=always", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// From inside a submodule the walk ends at the superproject, and from a plain
// repository at its own root.
func TestSuperprojectWalksOutOfASubmodule(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub, super := filepath.Join(root, "sub"), filepath.Join(root, "super")
	for _, dir := range []string{sub, super} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "init", "-q")
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "start")
	}
	git(t, super, "submodule", "add", "-q", sub, "vendored")
	nested := filepath.Join(super, "vendored")

	if got, err := gitroot.Superproject(nested, nil); err != nil || got != super {
		t.Fatalf("from the submodule = %q, %v; want %q", got, err, super)
	}
	if got, err := gitroot.Superproject(sub, nil); err != nil || got != sub {
		t.Fatalf("from a plain repository = %q, %v; want %q", got, err, sub)
	}
	if _, err := gitroot.Superproject(root, nil); err == nil {
		t.Fatal("outside any repository should fail")
	}
}

// A caller's own runner is what the walk uses.
func TestSuperprojectUsesTheCallersRunner(t *testing.T) {
	var calls []string
	run := func(dir string, args ...string) (string, error) {
		calls = append(calls, dir+" "+args[len(args)-1])
		if dir == "/w/super/sub" && args[1] == "--show-superproject-working-tree" {
			return "/w/super", nil
		}
		if args[1] == "--show-toplevel" {
			return dir, nil
		}
		return "", nil
	}
	got, err := gitroot.Superproject("/w/super/sub", run)
	want := []string{"/w/super/sub --show-superproject-working-tree", "/w/super --show-superproject-working-tree", "/w/super --show-toplevel"}
	if err != nil || got != "/w/super" || !slices.Equal(calls, want) {
		t.Fatalf("got %q, %v after %q", got, err, calls)
	}
}
