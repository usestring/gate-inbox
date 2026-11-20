package pigate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/piauto"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestConfiguredOperationRules(t *testing.T) {
	cfg := config.PiOperationRules{
		LinearIssueCreate: "/trusted/linear-create",
		PullRequestCreate: "/trusted/pr-create",
		PushRepositories:  []string{"Example/Project"},
	}
	sha := strings.Repeat("a", 40)
	baseTip := strings.Repeat("b", 40)
	gitAndGitHub := func(program string, args ...string) string {
		switch program + " " + strings.Join(args, " ") {
		case "git remote get-url --all origin", "git remote get-url --push --all origin":
			return "https://github.com/example/project.git"
		case "git symbolic-ref HEAD":
			return "refs/heads/feature"
		case "gh api --hostname github.com repos/example/project --jq " + githubPushMetadata:
			return "example/project\tmain\ttrue"
		case "git config --default off --get push.followTags",
			"git config --default off --get remote.origin.mirror",
			"git config --default off --get push.recurseSubmodules":
			return "off"
		case "git rev-parse --verify HEAD^{commit}":
			return sha
		case "gh api --hostname github.com repos/example/project/git/ref/heads/main --jq .object.sha":
			return baseTip
		case "git rev-list " + baseTip + ".." + sha:
			return sha
		case "git cat-file commit " + sha:
			return "tree hash\ngpgsig signature\n\nMessage"
		default:
			t.Fatalf("unexpected inspection: %s %v", program, args)
			return ""
		}
	}

	pr := "/trusted/pr-create --title 'Add a test' --body-file /tmp/pr.md"
	linear := `'/trusted/linear-create' --team ENG --title "Fix empty list" --body 'Keep $literal, pipes |, and \backslashes.'`
	for _, tt := range []struct {
		command string
		want    []store.PiOperationRule
	}{
		{linear, []store.PiOperationRule{store.PiLinearIssueCreate}},
		{pr, []store.PiOperationRule{store.PiPullRequestCreate}},
		{pr + " --push", []store.PiOperationRule{store.PiPullRequestCreate, store.PiRepositoryPush}},
		{"git push origin " + sha + ":refs/heads/feature", []store.PiOperationRule{store.PiRepositoryPush}},
		{linear + " && printf later", nil},
		{linear + " >output", nil},
		{linear + " &", nil},
		{linear + "\nprintf later", nil},
		{strings.Replace(linear, "Fix empty list", "$(printf title)", 1), nil},
		{"TOKEN=from-model " + linear, nil},
		{"pr-create --title foo --body bar", nil},
		{pr + " --disclosure-approval-file /tmp/claim", nil},
		{pr + " --push --repo example/other", nil},
		{pr + " --push --head main", nil},
		{pr + " --push --base feature", nil},
		{"git push origin +" + sha + ":refs/heads/feature", nil},
		{"git push origin " + sha + ":refs/heads/deploy-prod", nil},
		{"git push origin " + sha + ":refs/heads/main", nil},
		{"git push origin " + sha + ":refs/heads/feature --follow-tags", nil},
		{"git push origin :refs/heads/feature", nil},
		{"git push origin HEAD:refs/heads/feature", nil},
	} {
		t.Run(tt.command, func(t *testing.T) {
			var got []store.PiOperationRule
			if words := literalCommand(tt.command); len(words) > 0 {
				got = operationRules(cfg, words, gitAndGitHub)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
	for _, tt := range []struct {
		name, program, value string
		args                 string
	}{
		{"multiple push remotes", "git", "https://github.com/example/project.git\nhttps://github.com/other/target.git", "remote get-url --push --all origin"},
		{"custom default branch", "gh", "example/project\tfeature\ttrue", "api --hostname github.com repos/example/project --jq " + githubPushMetadata},
		{"public repository", "gh", "example/project\tmain\tfalse", "api --hostname github.com repos/example/project --jq " + githubPushMetadata},
		{"unsigned commit", "git", "tree hash\n\ngpgsig appears only in the message", "cat-file commit " + sha},
		{"tag following", "git", "true", "config --default off --get push.followTags"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			inspect := func(program string, args ...string) string {
				if program == tt.program && strings.Join(args, " ") == tt.args {
					return tt.value
				}
				return gitAndGitHub(program, args...)
			}
			if rules := operationRules(cfg, literalCommand("git push origin "+sha+":refs/heads/feature"), inspect); len(rules) != 0 {
				t.Fatalf("unexpected rules %v", rules)
			}
		})
	}
	cfg.PushRepositories = nil
	if got := operationRules(cfg, literalCommand(pr+" --push"), gitAndGitHub); len(got) != 0 {
		t.Errorf("push without opt-in got %v", got)
	}
}

func TestOperationRulesRespectExplicitPolicyAndPrecedeModel(t *testing.T) {
	cwd := t.TempDir()
	req := bashReq(cwd, "/trusted/pr-create --title example --body example")
	env := Env{
		Rules: []Rules{}, Grants: []grant.Grant{}, Approvals: []string{},
		OperationRules: func(grant.Call) []store.PiOperationRule { return []store.PiOperationRule{store.PiPullRequestCreate} },
		AutoMode: func(piauto.Request) piauto.Answer {
			t.Fatal("the model was consulted")
			return piauto.Answer{}
		},
	}
	if d := Decide(env, req); d.Decision != Allow || !slices.Equal(d.OperationRules, []store.PiOperationRule{store.PiPullRequestCreate}) {
		t.Fatalf("expected operation rule, got %+v", d)
	}
	env.Rules = sources(t, cwd, map[string]map[string][]string{"user settings": {"ask": {"Bash(/trusted/pr-create:*)"}}})
	if d := Decide(env, req); d.Decision != Ask || len(d.OperationRules) != 0 {
		t.Fatalf("expected explicit ask, got %+v", d)
	}
	env.Rules = []Rules{}
	req.ToolInput["dangerouslyDisableSandbox"] = true
	if d := Decide(env, req); d.Decision != Ask || len(d.OperationRules) != 0 {
		t.Fatalf("expected sandbox approval, got %+v", d)
	}
}

func TestHookPersistsOperationCounts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("GATE_INBOX_SKIP_PERMISSIONS", "")
	cfg := "[pi_operation_rules]\nlinear_issue_create = '/trusted/linear-create'\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cwd := t.TempDir()
	for _, command := range []string{"ls", "rg 'test.*pattern' .", "/trusted/linear-create --team ENG --title Test --body Test", "git branch --list", "tree -o notes"} {
		raw, err := json.Marshal(bashReq(cwd, command))
		if err != nil {
			t.Fatal(err)
		}
		Hook(strings.NewReader(string(raw)), dir, "fixture", "", nowForTest)
	}
	counts, err := db.PiOperationRuleCounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 4 || counts[store.PiReadOnly] != 3 || counts[store.PiLinearIssueCreate] != 1 ||
		counts[store.PiPullRequestCreate] != 0 || counts[store.PiRepositoryPush] != 0 {
		t.Fatalf("unexpected counts %v", counts)
	}
}

func TestReadOnlyRuleRejectsOptionsThatWriteOrExecute(t *testing.T) {
	for _, command := range []string{
		"git branch new-branch", "git branch -d to-delete", "git diff --output=local",
		"rg --pre=helper match", "tree -o output", "tree -aooutput",
	} {
		if readOnlyCommand(grant.Call{Cwd: t.TempDir(), Tool: "Bash", Input: map[string]any{"command": command}}) {
			t.Errorf("%q was treated as read-only", command)
		}
	}
}
