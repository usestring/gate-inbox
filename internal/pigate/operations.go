package pigate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/workspec"
	"mvdan.cc/sh/v3/syntax"
)

func configuredOperationRules(configDir string, call grant.Call) []store.PiOperationRule {
	command, _ := call.Input["command"].(string)
	if call.Tool != "Bash" || !filepath.IsAbs(call.Cwd) || command == "" {
		return nil
	}
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return nil
	}
	rules := cfg.PiOperationRules
	if rules.LinearIssueCreate == "" && rules.PullRequestCreate == "" && len(rules.PushRepositories) == 0 {
		return nil
	}
	words := literalCommand(command)
	if len(words) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return operationRules(rules, words, func(program string, args ...string) string {
		if program == "git" {
			args = append([]string{"--no-replace-objects"}, args...)
		}
		cmd := exec.CommandContext(ctx, program, args...)
		cmd.Dir = call.Cwd
		cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	})
}

type inspectCommand func(program string, args ...string) string

func operationRules(cfg config.PiOperationRules, words []string, inspect inspectCommand) []store.PiOperationRule {
	if len(words) == 4 && words[0] == "git" && words[1] == "push" && words[2] == "origin" {
		if checkedBranchPush(cfg.PushRepositories, words[3], inspect) {
			return []store.PiOperationRule{store.PiRepositoryPush}
		}
		return nil
	}
	// PATH lookups and alternate spellings could select an executable from
	// the worktree instead of the operator's trusted copy.
	if !filepath.IsAbs(words[0]) {
		return nil
	}
	switch words[0] {
	case cfg.LinearIssueCreate:
		flags, ok := createFlags(words[1:], []string{
			"--team", "--title", "--body", "--state", "--parent", "--assignee",
			"--assignee-email", "--project", "--repo", "--milestone", "--label",
		}, nil)
		if ok && flags["--team"] != "" && flags["--title"] != "" && flags["--body"] != "" && flags["--body"] != "-" {
			return []store.PiOperationRule{store.PiLinearIssueCreate}
		}
	case cfg.PullRequestCreate:
		flags, ok := createFlags(words[1:], []string{
			"--repo", "--title", "--body", "--body-file", "--head", "--base",
			"--assignee", "--merge-dependency",
		}, []string{"--draft", "--ui-evidence-required", "--push"})
		if !ok || flags["--title"] == "" || (flags["--body"] == "" && flags["--body-file"] == "") {
			return nil
		}
		if flags["--push"] == "" {
			return []store.PiOperationRule{store.PiPullRequestCreate}
		}
		if checkedPRPush(cfg.PushRepositories, flags, inspect) {
			return []store.PiOperationRule{store.PiPullRequestCreate, store.PiRepositoryPush}
		}
	}
	return nil
}

func createFlags(args, valueFlags, switches []string) (map[string]string, bool) {
	flags := make(map[string]string)
	for len(args) != 0 {
		name := args[0]
		args = args[1:]
		if _, duplicate := flags[name]; duplicate && name != "--label" {
			return nil, false
		}
		switch {
		case slices.Contains(switches, name):
			flags[name] = "true"
		case slices.Contains(valueFlags, name) && len(args) != 0:
			flags[name], args = args[0], args[1:]
		default:
			return nil, false
		}
	}
	return flags, true
}

func checkedPRPush(repositories []string, flags map[string]string, inspect inspectCommand) bool {
	target := pushTarget(repositories, inspect)
	if target.repo == "" || (flags["--head"] != "" && flags["--head"] != target.branch) {
		return false
	}
	if repo := flags["--repo"]; repo != "" && !strings.EqualFold(target.repo, repo) {
		return false
	}
	// The wrapper uses its base to inspect outgoing commits and disclosure.
	if base := flags["--base"]; base != target.base && (base != "" || target.base != "main") {
		return false
	}
	return true
}

func checkedBranchPush(repositories []string, refspec string, inspect inspectCommand) bool {
	source, destination, ok := strings.Cut(refspec, ":")
	if !ok || !commitHash.MatchString(source) || !strings.HasPrefix(destination, "refs/heads/") {
		return false
	}
	target := pushTarget(repositories, inspect)
	if target.repo == "" || destination != "refs/heads/"+target.branch ||
		source != inspect("git", "rev-parse", "--verify", "HEAD^{commit}") {
		return false
	}
	// Public repositories need the disclosure check in the PR wrapper.
	if !target.private {
		return false
	}
	// Compare against the live base so an old or locally changed tracking
	// ref cannot hide commits the push would publish.
	baseTip := inspect("gh", "api", "--hostname", "github.com", "repos/"+target.repo+"/git/ref/heads/"+target.base, "--jq", ".object.sha")
	if !commitHash.MatchString(baseTip) {
		return false
	}
	outgoing := strings.Fields(inspect("git", "rev-list", baseTip+".."+source))
	if len(outgoing) == 0 {
		return false
	}
	for _, sha := range outgoing {
		if !commitHash.MatchString(sha) {
			return false
		}
		header, _, _ := strings.Cut(inspect("git", "cat-file", "commit", sha), "\n\n")
		if !strings.Contains(header, "\ngpgsig ") {
			return false
		}
	}
	return true
}

type branchPushTarget struct {
	repo, branch, base string
	private            bool
}

const githubPushMetadata = `[.full_name, .default_branch, .private] | @tsv`

func pushTarget(repositories []string, inspect inspectCommand) branchPushTarget {
	if len(repositories) == 0 {
		return branchPushTarget{}
	}
	repo := githubRepository(inspect("git", "remote", "get-url", "--all", "origin"))
	pushRepo := githubRepository(inspect("git", "remote", "get-url", "--push", "--all", "origin"))
	if repo == "" || repo != pushRepo || !slices.ContainsFunc(repositories, func(allowed string) bool {
		return strings.EqualFold(repo, allowed)
	}) {
		return branchPushTarget{}
	}
	branch, ok := strings.CutPrefix(inspect("git", "symbolic-ref", "HEAD"), "refs/heads/")
	if !ok || branch == "" || branch == "main" || branch == "master" || strings.HasPrefix(branch, "deploy-") {
		return branchPushTarget{}
	}
	// Use the live default branch; origin/HEAD can be stale or missing.
	metadata := strings.Split(inspect("gh", "api", "--hostname", "github.com", "repos/"+repo, "--jq", githubPushMetadata), "\t")
	if len(metadata) != 3 || !strings.EqualFold(metadata[0], repo) ||
		metadata[1] == "" || metadata[1] == branch || (metadata[2] != "true" && metadata[2] != "false") {
		return branchPushTarget{}
	}
	// The explicit refspec must be the only thing pushed.
	for _, key := range []string{"push.followTags", "remote.origin.mirror", "push.recurseSubmodules"} {
		value := inspect("git", "config", "--default", "off", "--get", key)
		if value != "off" && value != "false" && value != "no" && value != "0" {
			return branchPushTarget{}
		}
	}
	return branchPushTarget{repo: repo, branch: branch, base: metadata[1], private: metadata[2] == "true"}
}

var commitHash = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

var githubName = regexp.MustCompile(`^[a-zA-Z0-9_-]+/[a-zA-Z0-9_.-]+$`)

func githubRepository(remote string) string {
	if !strings.HasPrefix(remote, "https://github.com/") {
		ssh, url := strings.CutPrefix(remote, "ssh://")
		login, hostAndPath, ok := strings.Cut(ssh, "@")
		separator := ":"
		if url {
			separator = "/"
		}
		if !ok || login != "git" || !strings.HasPrefix(hostAndPath, "github.com"+separator) {
			return ""
		}
	}
	repo := workspec.RepoFromRemote(remote)
	if !githubName.MatchString(repo) {
		return ""
	}
	return strings.ToLower(repo)
}

// literalCommand accepts one Bash call with static arguments. Even a wrapper
// with a trusted path cannot cover a second command or a shell expansion.
func literalCommand(command string) []string {
	if strings.ContainsRune(command, 0) {
		return nil
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) != 1 {
		return nil
	}
	stmt := file.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(stmt.Redirs) != 0 || stmt.Negated ||
		stmt.Semicolon.IsValid() || stmt.Background || stmt.Coprocess {
		return nil
	}
	return literalArgs(call)
}

func literalArgs(call *syntax.CallExpr) []string {
	words := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		var word strings.Builder
		if !literalWord(&word, arg.Parts, false) {
			return nil
		}
		words = append(words, word.String())
	}
	return words
}

func literalWord(out *strings.Builder, parts []syntax.WordPart, quoted bool) bool {
	for _, part := range parts {
		switch part := part.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(part.Value, "\\!") || (!quoted && strings.ContainsAny(part.Value, "*?[]{}~")) {
				return false
			}
			out.WriteString(part.Value)
		case *syntax.SglQuoted:
			if part.Dollar {
				return false
			}
			out.WriteString(part.Value)
		case *syntax.DblQuoted:
			if part.Dollar || !literalWord(out, part.Parts, true) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
