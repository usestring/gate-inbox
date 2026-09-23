// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package git shells out to the git CLI to resolve the repository and
// superproject roots a session's directory sits in.
package git

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/deps"
	"github.com/usestring/gate-inbox/internal/tracing"
)

var ErrNotARepo = errors.New("not a git repository")

type Driver struct {
	bin string
}

func New() (*Driver, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git not found in PATH: %w\n%s", err, deps.Hint("git"))
	}
	return &Driver{bin: bin}, nil
}

func (d *Driver) run(dir string, args ...string) (output string, err error) {
	// Every call here is a forked process, which is the only thing in this
	// package that costs anything. The span names the subcommand and the
	// working directory because the answer to "why was that slow" is nearly
	// always one of the two: a rev-parse walking out of a submodule, or a
	// directory on a filesystem that is not answering.
	//
	// The arguments past the subcommand are paths and refs. They say no more
	// about the cost, and a span is not somewhere to put a repository's
	// contents.
	if tracing.Enabled() {
		started := time.Now()
		subcommand := ""
		if len(args) > 0 {
			subcommand = args[0]
		}
		defer func() {
			tracing.Record("git.run", started, time.Now(), err,
				tracing.Attr{Key: "git.subcommand", Value: subcommand},
				tracing.Attr{Key: "dir", Value: dir})
		}()
	}
	cmd := exec.Command(d.bin, append([]string{"-c", "core.quotepath=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimRight(string(out), "\n")
	if err != nil {
		if strings.Contains(text, "not a git repository") {
			return "", ErrNotARepo
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, text)
	}
	return text, nil
}

func (d *Driver) RepoRoot(dir string) (string, error) {
	top, err := d.run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %s", dir)
	}
	return top, nil
}

// SuperprojectRoot is the outermost working tree containing dir: the
// superproject when dir is inside a submodule, and the repository root
// otherwise. A linked worktree is its own superproject, not the checkout it
// was created from.
//
// It exists because a repository's own boundary is not where a person keeps
// things. Work spanning a submodule is done from the superproject, and the
// files that belong to the task as a whole -- notes, plans, a brief
// -- sit at its root, above the submodule the editing happens in.
func (d *Driver) SuperprojectRoot(dir string) (string, error) {
	// The flag prints nothing and succeeds when dir is not in a submodule,
	// which is why an empty result ends the walk rather than erroring. It
	// names the immediate parent, so nested submodules take a hop each; the
	// bound is there because this is a loop over an answer git gives us.
	current := dir
	for range maxSubmoduleDepth {
		super, err := d.run(current, "rev-parse", "--show-superproject-working-tree")
		if err != nil || super == "" {
			break
		}
		current = super
	}
	return d.RepoRoot(current)
}

// maxSubmoduleDepth bounds the walk out of nested submodules. Two is the
// deepest nesting in use; the rest is headroom, and the bound is what keeps a
// surprising git answer from becoming a loop.
const maxSubmoduleDepth = 8

func (d *Driver) IsRepoRoot(dir string) bool {
	top, err := d.run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	resolvedTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return false
	}
	return resolvedDir == resolvedTop
}
