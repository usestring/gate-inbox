// Package gitroot finds the outermost working tree a directory belongs to,
// walking out of nested submodules.
package gitroot

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Runner runs git in dir and returns its trimmed standard output. A caller
// with its own git wrapper -- traced, or on a pinned binary -- passes it; nil
// runs the git on PATH with a timeout.
type Runner func(dir string, args ...string) (string, error)

// Superproject is the outermost working tree containing dir: the
// superproject when dir is inside a submodule, and the repository root
// otherwise. A linked worktree is its own superproject, not the checkout it
// was created from.
//
// It exists because a repository's own boundary is not where a person keeps
// things. Work spanning a submodule is done from the superproject, and the
// files that belong to the task as a whole -- notes, plans, a brief
// -- sit at its root, above the submodule the editing happens in.
func Superproject(dir string, run Runner) (string, error) {
	if run == nil {
		run = execGit
	}
	// The flag prints nothing and succeeds when dir is not in a submodule,
	// which is why an empty result ends the walk rather than erroring. It
	// names the immediate parent, so nested submodules take a hop each; the
	// bound is there because this is a loop over an answer git gives us.
	current := dir
	for range maxSubmoduleDepth {
		super, err := run(current, "rev-parse", "--show-superproject-working-tree")
		if err != nil || super == "" {
			break
		}
		current = super
	}
	return run(current, "rev-parse", "--show-toplevel")
}

// maxSubmoduleDepth bounds the walk out of nested submodules. Two is the
// deepest nesting in use; the rest is headroom, and the bound is what keeps a
// surprising git answer from becoming a loop.
const maxSubmoduleDepth = 8

// gitTimeout bounds one git call: git on a wedged network filesystem is the
// one way this lookup can hang.
const gitTimeout = 10 * time.Second

func execGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
