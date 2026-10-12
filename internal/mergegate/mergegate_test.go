package mergegate

import (
	"testing"
)

var testConfig = Config{
	Owners:       []string{"usestring"},
	Authors:      []string{"merge-author"},
	ExcludeRepos: []string{"usestring/partiful"},
}

func TestParseAdmitsOnlyTheCoveredShape(t *testing.T) {
	for command, want := range map[string]Merge{
		"gh pr merge 331 --squash --delete-branch":                                                  {Number: 331},
		"gh pr merge 331 -s -d -R usestring/gate-inbox":                                             {Number: 331, Repo: "usestring/gate-inbox"},
		"cd /work/checkout && gh pr merge 8343 --squash --repo usestring/gate-inbox 2>&1 | tail -3": {Number: 8343, Repo: "usestring/gate-inbox"},
		"gh pr merge 1 --squash --match-head-commit abc1234":                                        {Number: 1},
	} {
		got, ok := Parse(command, "/work")
		if !ok {
			t.Errorf("Parse(%q) refused, want admit", command)
			continue
		}
		if got.Number != want.Number || (want.Repo != "" && got.Repo != want.Repo) {
			t.Errorf("Parse(%q) = %+v, want number %d repo %q", command, got, want.Number, want.Repo)
		}
	}
	for _, command := range []string{
		"gh pr merge 331 --squash --delete-branch --admin",
		"gh pr merge 331 --squash --admin",
		"gh pr merge 331 --merge",
		"gh pr merge 331 --squash --body note",
		"GH_TOKEN=x gh pr merge 331 --squash",
		"gh pr merge 331 --squash; gh pr view 331",
		"gh pr merge 0 --squash",
		"gh pr merge --squash",
		"echo gh pr merge 331 --squash",
		"gh pr merge 331 --squash | grep green",
		"gh pr merge 331 --squash --repo evil repo",
		"gh pr merge 331",
	} {
		if _, ok := Parse(command, "/work"); ok {
			t.Errorf("Parse(%q) admitted, want refuse", command)
		}
	}
}

func TestJudgeAdmitsOnlyAGreenOwnedPullRequest(t *testing.T) {
	green := pull{State: "OPEN", BaseRefName: "main", MergeStateStatus: "CLEAN",
		StatusCheckRollup: []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		}{{Status: "COMPLETED", Conclusion: "SUCCESS"}}}
	green.Author.Login = "merge-author"
	if err := judge(testConfig, green, "main"); err != nil {
		t.Fatalf("green PR refused: %v", err)
	}
	for name, mutate := range map[string]func(*pull){
		"closed":        func(p *pull) { p.State = "MERGED" },
		"draft":         func(p *pull) { p.IsDraft = true },
		"another's":     func(p *pull) { p.Author.Login = "someone-else" },
		"off branch":    func(p *pull) { p.BaseRefName = "feature" },
		"deploy branch": func(p *pull) { p.BaseRefName = "deploy-x" },
		"blocked":       func(p *pull) { p.MergeStateStatus = "BLOCKED" },
		"no checks":     func(p *pull) { p.StatusCheckRollup = nil },
		"red check":     func(p *pull) { p.StatusCheckRollup[0].Conclusion = "FAILURE" },
	} {
		bad := green
		bad.StatusCheckRollup = append([]struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		}{}, green.StatusCheckRollup...)
		mutate(&bad)
		if name == "deploy branch" {
			if err := judge(testConfig, bad, "deploy-x"); err == nil {
				t.Errorf("%s admitted, want refuse", name)
			}
			continue
		}
		if err := judge(testConfig, bad, "main"); err == nil {
			t.Errorf("%s admitted, want refuse", name)
		}
	}
}
