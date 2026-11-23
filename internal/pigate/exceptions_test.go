package pigate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/piauto"
)

// exceptionsEnv is a deny-by-exception gate with no rules, grants or
// approvals, whose classifier gives every call the danger score given, or
// fails when danger is negative.
func exceptionsEnv(danger float64, asked *int) Env {
	return Env{Rules: []Rules{}, Grants: []grant.Grant{}, Approvals: []string{},
		DenyByException: true,
		AutoMode: func(req piauto.Request) piauto.Answer {
			*asked++
			if req.Question != piauto.QuestionDanger {
				return piauto.Answer{Judged: true, Safe: 0.99, Allow: true}
			}
			if danger < 0 {
				return piauto.Answer{Note: "the JEV classifier did not answer"}
			}
			return piauto.Answer{Judged: true, Question: piauto.QuestionDanger, Danger: danger,
				Allow: danger < piauto.DangerThreshold}
		}}
}

// The calls session 5341a349 was asked about, all sandboxed reads, run
// without a dialog once the gate asks only about danger.
func TestDenyByExceptionRunsTheReadsThatUsedToAsk(t *testing.T) {
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	reads := []string{
		`sqlite3 ~/.config/gate-inbox/state.db ".tables"`,
		"python3 - <<'EOF'\nimport sqlite3\ndb = sqlite3.connect('state.db')\nprint(db.execute('select count(*) from sessions').fetchall())\nEOF",
		"cd sub && ls; du -sh .",
		"go test ./...",
		"rm -rf build " + filepath.Join(os.TempDir(), "scratch"),
	}
	for _, command := range reads {
		// The classifier finds no danger.
		asked := 0
		if d := Decide(exceptionsEnv(0.04, &asked), bashReq(cwd, command)); d.Decision != Allow || asked != 1 {
			t.Errorf("%q: %+v (classifier asked %d)", command, d, asked)
		}
		// The allow-list gate still asks about the same call, at safe 0.83.
		env := exceptionsEnv(0.04, &asked)
		env.DenyByException = false
		env.AutoMode = func(piauto.Request) piauto.Answer {
			return piauto.Answer{Judged: true, Safe: 0.83, Note: "JEV in_scope 0.75, safe 0.83 → asking"}
		}
		if d := Decide(env, bashReq(cwd, command)); d.Decision != Ask {
			t.Errorf("allow-list %q: %+v", command, d)
		}
	}
}

// Each danger class still asks: by the deterministic list without a
// classifier call, whatever the classifier would have said, or by the
// classifier's own confident verdict.
func TestDenyByExceptionStillAsksAboutEachDangerClass(t *testing.T) {
	cwd := t.TempDir()
	// A home outside the temporary directory, as a real one is.
	home := filepath.Join(string(filepath.Separator), "home", "gate-inbox-test-user")
	listed := []Request{
		// irreversible deletion
		bashReq(cwd, "rm -rf ~/projects"),
		bashReq(cwd, "rm -rf /"),
		bashReq(cwd, "cd /tmp && rm -rf "+home),
		bashReq(cwd, "find / -name '*.log' -delete"),
		bashReq(cwd, "git reset --hard origin/main"),
		bashReq(cwd, "git clean -fdx"),
		bashReq(cwd, `bash -c "rm -rf $HOME"`),
		bashReq(cwd, "sudo rm x"),
		// shared-branch and force pushes
		bashReq(cwd, "git push --force origin feat"),
		bashReq(cwd, "git push -f"),
		bashReq(cwd, "git push origin +feat"),
		bashReq(cwd, "git push origin main"),
		bashReq(cwd, "git push origin HEAD:deploy-web"),
		bashReq(cwd, "git push origin origin/main:refs/heads/deploy-api"),
		bashReq(cwd, "git push origin --delete feat"),
		// production and cloud writes, costly operations
		bashReq(cwd, "kubectl delete pod web-1"),
		bashReq(cwd, "kubectl apply -f deploy.yaml"),
		bashReq(cwd, "terraform apply -auto-approve"),
		bashReq(cwd, "terraform destroy"),
		bashReq(cwd, "gcloud run deploy api --image x"),
		bashReq(cwd, "gcloud compute instances delete vm-1"),
		bashReq(cwd, "gsutil rm gs://bucket/x"),
		bashReq(cwd, "aws s3 rm s3://bucket/x"),
		bashReq(cwd, "helm upgrade api ./chart"),
		// outbound messages and publishing
		bashReq(cwd, "curl -X POST https://hooks.slack.com/services/x -d '{}'"),
		bashReq(cwd, "curl --data @body.json https://api.example.com/v1/send"),
		bashReq(cwd, "wget --post-data=x https://example.com"),
		bashReq(cwd, "sendmail ops < note.txt"),
		bashReq(cwd, "./skills/send-slack-message/scripts/send.sh '#general' hi"),
		bashReq(cwd, "npm publish"),
		bashReq(cwd, "docker push ghcr.io/x/y"),
		bashReq(cwd, "gh pr comment 12 --body hi"),
		bashReq(cwd, "gh api -X POST repos/x/y/issues -f title=t"),
		bashReq(cwd, "ssh prod-1 uptime"),
		// security, permission and credential changes
		bashReq(cwd, "echo key >> ~/.ssh/authorized_keys"),
		bashReq(cwd, "cp creds.json ~/.config/gcloud/application_default_credentials.json"),
		bashReq(cwd, "cat ~/.aws/credentials"),
		bashReq(cwd, "chmod 777 /etc/hosts"),
		bashReq(cwd, "git config --global core.hooksPath /tmp/x"),
		bashReq(cwd, "crontab job.txt"),
		{ToolName: "write", Cwd: cwd, ToolInput: map[string]any{"path": filepath.Join(home, ".bashrc"), "content": "x"}},
		{ToolName: "edit", Cwd: cwd, ToolInput: map[string]any{"path": "~/.ssh/config", "edits": []any{}}},
		{ToolName: "read", Cwd: cwd, ToolInput: map[string]any{"path": "~/.ssh/id_ed25519"}},
		// outbound MCP tools that are not marked read-only
		{ToolName: "mcp__hermes_send_whatsapp", MCPServer: "hermes", MCPTool: "send_whatsapp", Cwd: cwd, ToolInput: map[string]any{"to": "x"}},
		{ToolName: "mcp__slack_post_message", MCPServer: "slack", MCPTool: "post_message", Cwd: cwd, ToolInput: map[string]any{}},
		{ToolName: "mcp__gate_inbox_board_grant_permission", MCPServer: "gate-inbox:board", MCPTool: "grant_permission", Cwd: cwd, ToolInput: map[string]any{}},
	}
	for _, req := range listed {
		for _, danger := range []float64{0.01, -1} {
			asked := 0
			env := exceptionsEnv(danger, &asked)
			env.Home = home
			d := Decide(env, req)
			if d.Decision != Ask || asked != 0 || !strings.HasPrefix(d.Reason, "Gate Inbox asks about ") {
				t.Errorf("%s %v (danger %v): %+v (classifier asked %d)", req.ToolName, req.ToolInput, danger, d, asked)
			}
		}
	}
	// What the list cannot see, the classifier can: a confident danger asks.
	asked := 0
	d := Decide(exceptionsEnv(0.92, &asked), bashReq(cwd, "python3 scripts/notify_customers.py --all"))
	if d.Decision != Ask || asked != 1 || !strings.Contains(d.Reason, "JEV danger 0.92") {
		t.Errorf("classifier danger: %+v", d)
	}
}

// What takes precedence over the default allow still does.
func TestDenyByExceptionKeepsRulesGatesAndTheSandbox(t *testing.T) {
	cwd := t.TempDir()
	asked := 0
	env := exceptionsEnv(0.01, &asked)
	env.Rules = sources(t, cwd, map[string]map[string][]string{"user settings": {
		"deny": {"Bash(make clean:*)"},
		"ask":  {"Bash(make release:*)"},
	}})
	for _, c := range []struct {
		req  Request
		want string
	}{
		{bashReq(cwd, "make clean"), Deny},
		{bashReq(cwd, "make release"), Ask},
		{bashReq(cwd, "gh pr merge 1 --squash"), Ask},
		{Request{ToolName: "bash", Cwd: cwd, ToolInput: map[string]any{"command": "ls", "dangerouslyDisableSandbox": true}}, Ask},
		{Request{ToolName: "AskUserQuestion", Cwd: cwd, ToolInput: map[string]any{}}, Allow},
	} {
		if d := Decide(env, c.req); d.Decision != c.want {
			t.Errorf("%v: %+v", c.req.ToolInput, d)
		}
	}
	if asked != 0 {
		t.Errorf("the classifier was asked %d times about calls already settled", asked)
	}
	// A grant still allows a listed danger, as an approval from a person.
	env.Grants = []grant.Grant{{Kind: grant.KindRule, Value: "Bash(git push:*)"}}
	if d := Decide(env, bashReq(cwd, "git push origin main")); d.Decision != Allow {
		t.Errorf("granted push: %+v", d)
	}
}

// When the classifier cannot answer, classifier_failure decides: "allow"
// runs what the deterministic list passes, "ask" asks.
func TestDenyByExceptionClassifierFailure(t *testing.T) {
	cwd := t.TempDir()
	asked := 0
	env := exceptionsEnv(-1, &asked)
	if d := Decide(env, bashReq(cwd, "go build ./...")); d.Decision != Allow || !strings.Contains(d.Reason, "classifier") {
		t.Errorf("failure allow: %+v", d)
	}
	env.FailureAsks = true
	if d := Decide(env, bashReq(cwd, "go build ./...")); d.Decision != Ask || !strings.Contains(d.Reason, "did not answer") {
		t.Errorf("failure ask: %+v", d)
	}
	// A read the fast path settles never needs the classifier.
	if d := Decide(env, bashReq(cwd, "ls")); d.Decision != Allow {
		t.Errorf("fast path: %+v", d)
	}
	// No classifier at all (auto mode off) is a failure too.
	env.AutoMode = nil
	if d := Decide(env, bashReq(cwd, "go build ./...")); d.Decision != Ask {
		t.Errorf("no classifier, failure ask: %+v", d)
	}
	env.FailureAsks = false
	if d := Decide(env, bashReq(cwd, "go build ./...")); d.Decision != Allow {
		t.Errorf("no classifier, failure allow: %+v", d)
	}
}

// The list is for the obvious cases: routine work passes it, and the usual
// ways of wrapping a listed command do not.
func TestDangerListPassesRoutineWorkAndSeesThroughWrappers(t *testing.T) {
	cwd := t.TempDir()
	call := func(command string) grant.Call {
		return grant.Call{Tool: "Bash", Input: map[string]any{"command": command}, Cwd: cwd,
			Home: filepath.Join(string(filepath.Separator), "home", "gate-inbox-test-user")}
	}
	defer func(saved func(string) string) { currentBranch = saved }(currentBranch)
	currentBranch = func(string) string { return "feat/x" }
	for _, command := range []string{
		"git push origin feat/x", "git push -u origin HEAD", "git push",
		"git status && git diff --stat", "git branch --list", "git config --global --get user.name",
		"gsutil cp gs://bucket/raw.json .", "gcloud logging read 'severity>=ERROR' --limit 10",
		"gcloud run services list", "kubectl get pods -n web", "terraform plan", "helm list",
		"curl -s https://example.com/api", "curl -X POST -d '{}' http://localhost:8080/hook", "http GET example.com",
		"gh pr view 12", "gh pr create --fill", "gh api repos/x/y/pulls", "gh run view 1 --log",
		"rm -rf node_modules dist", "find . -name '*.tmp' -delete", "chmod +x scripts/run.sh",
		"timeout 10 go test ./...", "env FOO=1 make", "npm test", "docker build -t x .", "crontab -l",
		"psql -c 'select count(*) from feeds'", "sqlite3 state.db .tables", "cat README.md | wc -l",
	} {
		if why, ok := dangerous(call(command)); ok {
			t.Errorf("%q listed: %s", command, why)
		}
	}
	currentBranch = func(string) string { return "main" }
	for _, command := range []string{
		"git push", "git push origin HEAD",
		"timeout 5 sudo ls", "env rm -rf /", "nohup rm -rf ~ &",
		"ls | xargs rm -f", `find . -exec sh -c 'rm -rf ~' \;`, "eval 'git push --force'",
		"sh -c \"$CMD\"", "rm -rf $DIR", "rm -rf .", "rm -rf /srv/other",
		"psql -c 'DELETE FROM feeds'", "find -L / -name x -delete", "gsutil cp out.json gs://bucket/",
		"http POST api.example.com name=x", "gh auth token", "gcloud auth print-access-token",
	} {
		if _, ok := dangerous(call(command)); !ok {
			t.Errorf("%q passed the list", command)
		}
	}
}

// The hook reads the gate's mode from the launch: deny by exception unless
// the tool says allowlist, with a typo read as the stricter allow list, and
// classifier_failure = "ask" asking when no classifier answers.
func TestHookReadsTheGateModeFromTheLaunch(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv(envname.SkipPermissions, "")
	in := `{"tool_name":"bash","tool_input":{"command":"sqlite3 state.db .tables"},"cwd":"` + cwd + `"}`
	for _, c := range []struct{ gate, failure, want string }{
		{"", "", Allow},
		{"exceptions", "allow", Allow},
		{"exceptions", "ask", Ask},
		{"", "typo", Ask},
		{"allowlist", "", Ask},
		{"exception", "", Ask},
	} {
		t.Setenv(envname.PiGate, c.gate)
		t.Setenv(envname.PiClassifierFailure, c.failure)
		var d Decision
		if err := json.Unmarshal([]byte(Hook(strings.NewReader(in), t.TempDir(), "s1", "", time.Now())), &d); err != nil {
			t.Fatal(err)
		}
		if d.Decision != c.want {
			t.Errorf("gate %q, classifier_failure %q: %+v, want %s", c.gate, c.failure, d, c.want)
		}
	}
}
