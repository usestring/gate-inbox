package sessioncmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

// A tool with a trust_flag (pi's --approve) gets it, and opens where it was
// asked, exactly when the caller vouches for the directory: its own tree, or
// a worktree of its repository such as a manager makes for a child. A
// repository the caller has nothing to do with, or one nested in its own,
// gets neither: the dialog shows and reads as waiting.
func TestTrustFlagGoesOnlyToDirectoriesTheCallerVouchesFor(t *testing.T) {
	root := t.TempDir()
	caller := filepath.Join(root, "repo")
	gitInit(t, caller)
	gitRun(t, caller, "-c", "user.email=t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	linked := filepath.Join(root, "trees", "child")
	gitRun(t, caller, "worktree", "add", "-q", "-b", "child", linked)
	sub := filepath.Join(caller, "pkg")
	nested := filepath.Join(caller, "vendor", "lib")
	stranger := filepath.Join(root, "other")
	for _, dir := range []string{sub, nested, stranger} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitInit(t, nested)
	gitInit(t, stranger)

	pi := config.Tool{TrustFlag: "--approve"}
	for _, c := range []struct {
		name, dir string
		flag      string
		launchIn  string
	}{
		{"the caller's own tree", sub, "--approve", sub},
		{"a worktree of the caller's repository", linked, "--approve", linked},
		{"a repository nested in the caller's", nested, "", caller},
		{"an unrelated repository", stranger, "", caller},
	} {
		if got := trustFlagFor(pi, caller, c.dir); got != c.flag {
			t.Errorf("%s: trust flag = %q, want %q", c.name, got, c.flag)
		}
		if got := launchDirectory(pi, caller, c.dir); got != c.launchIn {
			t.Errorf("%s: launch directory = %q, want %q", c.name, got, c.launchIn)
		}
	}
	if got := trustFlagFor(config.Tool{}, caller, linked); got != "" {
		t.Errorf("a tool with no trust_flag got %q", got)
	}
	if got := trustFlagFor(pi, "", linked); got != "" {
		t.Errorf("an operator spawn with no caller directory got %q", got)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
