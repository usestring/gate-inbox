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
	"github.com/usestring/gate-inbox/internal/parentseal"
)

func TestToolNameMapsPiToolsToClaudeNames(t *testing.T) {
	for pi, want := range map[string]string{
		"bash": "Bash", "read": "Read", "write": "Write", "edit": "Edit",
		"grep": "Grep", "find": "Glob", "ls": "LS", "glob": "Glob",
		"mcp__gate-inbox__report_status": "mcp__gate-inbox__report_status",
		// omp's spelling of the board's tools, through its plugin
		"mcp__gate_inbox_board_report_status": "mcp__gate-inbox__report_status",
		"my_tool":                             "my_tool",
	} {
		if got := ToolName(Request{ToolName: pi}); got != want {
			t.Errorf("ToolName(%q) = %q, want %q", pi, got, want)
		}
	}
}

// omp names an MCP tool mcp__<server>_<tool>, which cannot be split back, so
// the extension sends the server and tool its registration names, and the
// call is decided under Claude Code's name for it.
func TestToolNameGivesOmpMCPToolsClaudeNames(t *testing.T) {
	for _, c := range []struct{ omp, server, tool, want string }{
		{"mcp__my_srv_do_it", "my_srv", "do_it", "mcp__my_srv__do_it"},
		{"mcp__string_web_access_web_access_fetch", "string-web-access", "web_access_fetch", "mcp__string-web-access__web_access_fetch"},
		{"mcp__acme_tools_lookup", "acme:tools", "lookup", "mcp__plugin_acme_tools__lookup"},
		{"mcp__a_b_c_d_x_y", "a.b:c d", "x.y", "mcp__plugin_a_b_c_d__x_y"},
		{"mcp__gate_inbox_board_rename", "gate-inbox:board", "rename", "mcp__gate-inbox__rename"},
		// a server the board took from the Claude Code config keeps its name there
		{"mcp__gate_inbox_claude_mcp_string_web_access_web_access_fetch", "gate-inbox-claude-mcp:string-web-access", "web_access_fetch", "mcp__string-web-access__web_access_fetch"},
		// no registration to name them: omp's spelling, but the board's
		// prefix still maps
		{"mcp__my_srv_do_it", "", "", "mcp__my_srv_do_it"},
		{"mcp__gate_inbox_board_rename", "", "", "mcp__gate-inbox__rename"},
	} {
		if got := ToolName(Request{ToolName: c.omp, MCPServer: c.server, MCPTool: c.tool}); got != c.want {
			t.Errorf("ToolName(%q, %q, %q) = %q, want %q", c.omp, c.server, c.tool, got, c.want)
		}
	}
}

func TestDecideAppliesClaudeMCPRulesToOmpTools(t *testing.T) {
	cwd := t.TempDir()
	rules := sources(t, cwd, map[string]map[string][]string{"user settings": {
		"allow": {"mcp__my-srv__echo", "mcp__plugin_acme_tools", "mcp__wide__*"},
		"deny":  {"mcp__my-srv__drop"},
	}})
	omp := func(server, tool string) Request {
		return Request{ToolName: "mcp__" + server + "_" + tool, MCPServer: server, MCPTool: tool, ToolInput: map[string]any{}, Cwd: cwd}
	}
	for _, c := range []struct {
		req  Request
		want string
	}{
		{omp("my-srv", "echo"), Allow},
		{omp("my-srv", "other"), Ask},
		{omp("my-srv", "drop"), Deny},
		{omp("acme:tools", "lookup"), Allow},
		{omp("wide", "anything"), Allow},
		// omp's name alone matches no Claude rule, so it asks
		{Request{ToolName: "mcp__my_srv_echo", ToolInput: map[string]any{}, Cwd: cwd}, Ask},
	} {
		d := Decide(Env{Rules: rules, Grants: []grant.Grant{}, Approvals: []string{}}, c.req)
		if d.Decision != c.want {
			t.Errorf("%s (%s/%s) = %s (%s), want %s", c.req.ToolName, c.req.MCPServer, c.req.MCPTool, d.Decision, d.Reason, c.want)
		}
	}
}

func bashReq(cwd, command string) Request {
	return Request{ToolName: "bash", ToolInput: map[string]any{"command": command}, Cwd: cwd}
}

// sources writes one settings file per source label and returns their
// rules, as Load reads them.
func sources(t *testing.T, cwd string, files map[string]map[string][]string) []Rules {
	t.Helper()
	dir := t.TempDir()
	var srcs []Source
	for _, label := range []string{"managed settings", "user settings", "project settings", "local project settings"} {
		perms, ok := files[label]
		if !ok {
			continue
		}
		body, _ := json.Marshal(map[string]any{"permissions": perms})
		path := filepath.Join(dir, strings.ReplaceAll(label, " ", "-")+".json")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, Source{Label: label, Path: path, Base: cwd})
	}
	return Load(srcs)
}

func TestDecideDenyBeatsAskBeatsAllowAcrossSources(t *testing.T) {
	cwd := t.TempDir()
	labels := []string{"managed settings", "user settings", "project settings", "local project settings"}
	for _, denyAt := range labels {
		for _, allowAt := range labels {
			files := map[string]map[string][]string{}
			add := func(label, list, rule string) {
				if files[label] == nil {
					files[label] = map[string][]string{}
				}
				files[label][list] = append(files[label][list], rule)
			}
			add(allowAt, "allow", "Bash(rm -rf:*)")
			add(allowAt, "allow", "Bash(git push:*)")
			add(denyAt, "deny", "Bash(rm -rf:*)")
			add(denyAt, "ask", "Bash(git push --force:*)")
			env := Env{Rules: sources(t, cwd, files), Grants: []grant.Grant{}}
			if d := Decide(env, bashReq(cwd, "rm -rf build")); d.Decision != Deny || !strings.Contains(d.Reason, denyAt) {
				t.Errorf("deny in %s, allow in %s: rm -rf = %+v", denyAt, allowAt, d)
			}
			if d := Decide(env, bashReq(cwd, "git push --force origin main")); d.Decision != Ask {
				t.Errorf("ask in %s, allow in %s: git push --force = %+v", denyAt, allowAt, d)
			}
			if d := Decide(env, bashReq(cwd, "git push origin main")); d.Decision != Allow || !strings.Contains(d.Reason, allowAt) {
				t.Errorf("allow in %s: git push = %+v", allowAt, d)
			}
		}
	}
}

func TestDecideAppliesGrantKinds(t *testing.T) {
	cwd := t.TempDir()
	for _, c := range []struct {
		grant grant.Grant
		req   Request
		want  string
	}{
		{grant.Grant{Kind: grant.KindRule, Value: "Bash(git push origin:*)"}, bashReq(cwd, "git push origin x"), Allow},
		{grant.Grant{Kind: grant.KindRule, Value: "Bash(git push origin:*)"}, bashReq(cwd, "git push --force origin x"), Ask},
		{grant.Grant{Kind: grant.KindUnsandboxed, Value: "tools/probe.sh"}, Request{ToolName: "bash", Cwd: cwd,
			ToolInput: map[string]any{"command": "tools/probe.sh", "dangerouslyDisableSandbox": true}}, Allow},
		{grant.Grant{Kind: grant.KindRule, Value: "Bash(tools/probe.sh:*)"}, Request{ToolName: "bash", Cwd: cwd,
			ToolInput: map[string]any{"command": "tools/probe.sh", "dangerouslyDisableSandbox": true}}, Ask},
		// Neither a domain nor a soft grant allows a call.
		{grant.Grant{Kind: grant.KindDomain, Value: "api.example.com"}, Request{ToolName: "WebFetch", Cwd: cwd,
			ToolInput: map[string]any{"url": "https://api.example.com/"}}, Ask},
		{grant.Grant{Kind: grant.KindSoft, Value: "make deploy"}, bashReq(cwd, "make deploy"), Ask},
	} {
		d := Decide(Env{Rules: []Rules{}, Grants: []grant.Grant{c.grant}}, c.req)
		if d.Decision != c.want {
			t.Errorf("grant %s %q, %v: %+v, want %s", c.grant.Kind, c.grant.Value, c.req.ToolInput, d, c.want)
		}
	}
}

func TestDecideDenyRuleBeatsGrant(t *testing.T) {
	cwd := t.TempDir()
	env := Env{
		Rules:  sources(t, cwd, map[string]map[string][]string{"user settings": {"deny": {"Bash(git push:*)"}}}),
		Grants: []grant.Grant{{Kind: grant.KindRule, Value: "Bash(git push origin:*)"}},
	}
	if d := Decide(env, bashReq(cwd, "git push origin x")); d.Decision != Deny {
		t.Fatalf("a deny rule under a grant: %+v", d)
	}
}

func TestDecideReadOnlyFastPath(t *testing.T) {
	cwd := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cwd, "escape")); err != nil {
		t.Fatal(err)
	}
	env := Env{Rules: []Rules{}, Grants: []grant.Grant{}}
	for _, c := range []struct {
		req  Request
		want string
	}{
		{Request{ToolName: "read", ToolInput: map[string]any{"path": "src/a.go"}, Cwd: cwd}, Allow},
		{Request{ToolName: "read", ToolInput: map[string]any{"path": filepath.Join(cwd, "a.go")}, Cwd: cwd}, Allow},
		{Request{ToolName: "read", ToolInput: map[string]any{"path": filepath.Join(outside, "a.go")}, Cwd: cwd}, Ask},
		{Request{ToolName: "read", ToolInput: map[string]any{"path": "../x"}, Cwd: cwd}, Ask},
		{Request{ToolName: "read", ToolInput: map[string]any{"path": "~/x"}, Cwd: cwd}, Ask},
		{Request{ToolName: "read", ToolInput: map[string]any{"path": "escape/a.go"}, Cwd: cwd}, Ask},
		{Request{ToolName: "ls", ToolInput: map[string]any{}, Cwd: cwd}, Allow},
		{Request{ToolName: "grep", ToolInput: map[string]any{"pattern": "a.*b", "path": "."}, Cwd: cwd}, Allow},
		{Request{ToolName: "find", ToolInput: map[string]any{"pattern": "**/*.go"}, Cwd: cwd}, Allow},
		{Request{ToolName: "find", ToolInput: map[string]any{"pattern": "/etc/*"}, Cwd: cwd}, Ask},
		{Request{ToolName: "write", ToolInput: map[string]any{"path": "a.go", "content": "x"}, Cwd: cwd}, Ask},
		{Request{ToolName: "edit", ToolInput: map[string]any{"path": "a.go"}, Cwd: cwd}, Ask},
		{bashReq(cwd, "ls"), Allow},
		{bashReq(cwd, "ls -la src"), Allow},
		{bashReq(cwd, "git status"), Allow},
		{bashReq(cwd, "git log --oneline -5"), Allow},
		{bashReq(cwd, "cat /etc/passwd"), Ask},
		{bashReq(cwd, "cat ../x"), Ask},
		{bashReq(cwd, "ls escape"), Ask},
		{bashReq(cwd, "ls && rm -rf x"), Ask},
		{bashReq(cwd, "git push origin x"), Ask},
		{bashReq(cwd, "git -C /tmp status"), Ask},
		{bashReq(cwd, "rm x"), Ask},
		{Request{ToolName: "bash", ToolInput: map[string]any{"command": "ls", "dangerouslyDisableSandbox": true}, Cwd: cwd}, Ask},
		{Request{ToolName: "mcp__x__list", ToolInput: map[string]any{}, Cwd: cwd, ReadOnly: true}, Allow},
		{Request{ToolName: "mcp__x__delete", ToolInput: map[string]any{}, Cwd: cwd}, Ask},
		// The hint counts only for an MCP tool.
		{Request{ToolName: "my_tool", ToolInput: map[string]any{}, Cwd: cwd, ReadOnly: true}, Ask},
		{Request{ToolName: "codemode", ToolInput: map[string]any{"code": "x"}, Cwd: cwd}, Allow},
		// omp reaches an MCP tool it mounts behind xd:// by reading its docs
		// and writing its call, which comes back under the tool's own name.
		{Request{ToolName: "read", ToolInput: map[string]any{"path": "xd://mcp__x_delete"}, Cwd: cwd}, Allow},
		{Request{ToolName: "write", ToolInput: map[string]any{"path": "xd://mcp__x_delete", "content": "{}"}, Cwd: cwd}, Allow},
		// Any other device is omp's own, decided as the write it is.
		{Request{ToolName: "write", ToolInput: map[string]any{"path": "xd://report_issue", "content": "x"}, Cwd: cwd}, Ask},
	} {
		if d := Decide(env, c.req); d.Decision != c.want {
			t.Errorf("%s %v: %+v, want %s", c.req.ToolName, c.req.ToolInput, d, c.want)
		}
	}
}

func TestDecideRefusesTheSealingKeys(t *testing.T) {
	root := t.TempDir()
	keys := parentseal.KeyDir(root)
	cwd := t.TempDir()
	env := Env{ConfigDir: root, Rules: sources(t, cwd, map[string]map[string][]string{"user settings": {"allow": {"Bash", "Read"}}}),
		Grants: []grant.Grant{}}
	for _, req := range []Request{
		bashReq(cwd, "cat "+filepath.Join(keys, "typesafe.key")),
		{ToolName: "read", ToolInput: map[string]any{"path": filepath.Join(keys, "s1")}, Cwd: cwd},
	} {
		if d := Decide(env, req); d.Decision != Deny || !strings.Contains(d.Reason, "sealing-key") {
			t.Errorf("%v: %+v", req.ToolInput, d)
		}
	}
}

func TestDecideMergeGate(t *testing.T) {
	cwd := t.TempDir()
	rules := sources(t, cwd, map[string]map[string][]string{})
	covered := func(string, string) (string, bool) { return "covered", true }
	refused := func(string, string) (string, bool) { return "merge gate: PR #1 has a failing check", false }
	if d := Decide(Env{Rules: rules, Grants: []grant.Grant{}, MergeGate: covered}, bashReq(cwd, "gh pr merge 1 --squash")); d.Decision != Allow {
		t.Errorf("covered merge: %+v", d)
	}
	if d := Decide(Env{Rules: rules, Grants: []grant.Grant{}, MergeGate: refused}, bashReq(cwd, "gh pr merge 1 --squash")); d.Decision != Ask ||
		d.Reason != "merge gate: PR #1 has a failing check" {
		t.Errorf("refused merge: %+v, want the gate's own reason", d)
	}
	// A refusal is terminal: no grant, dialog approval or allow rule lets
	// the merge through, and the dialog offers no rule to remember.
	allowing := sources(t, cwd, map[string]map[string][]string{"user settings": {"allow": {"Bash(gh pr:*)", "Bash"}}})
	env := Env{Rules: allowing, MergeGate: refused,
		Grants: []grant.Grant{{Kind: grant.KindRule, Value: "Bash(gh pr merge:*)"}}}
	for _, cmd := range []string{"gh pr merge 1 --squash", "git fetch && gh pr merge 2"} {
		if d := Decide(env, bashReq(cwd, cmd)); d.Decision != Ask || d.Suggestion != "" {
			t.Errorf("%s with an allow rule, grant and approval: %+v, want an ask with no rule offered", cmd, d)
		}
	}
	if d := Decide(Env{Rules: allowing, Grants: []grant.Grant{}}, bashReq(cwd, "gh pr merge 1")); d.Decision != Ask {
		t.Errorf("no merge gate wired: %+v, want ask", d)
	}
}

// Skipped permissions allow what nothing else decided, as Claude Code's
// bypass mode does, and still keep every deny rule, ask rule and the merge
// gate.
func TestDecideSkipPermissionsKeepsRulesAndTheMergeGate(t *testing.T) {
	cwd := t.TempDir()
	rules := sources(t, cwd, map[string]map[string][]string{"user settings": {
		"deny": {"Bash(rm -rf:*)"}, "ask": {"Bash(git push:*)"}}})
	refused := func(string, string) (string, bool) { return "merge gate: PR #1 has a failing check", false }
	env := Env{Rules: rules, Grants: []grant.Grant{}, MergeGate: refused, SkipPermissions: true}
	outside := Request{ToolName: "bash", Cwd: cwd, ToolInput: map[string]any{"command": "make", "dangerouslyDisableSandbox": true}}
	for _, c := range []struct {
		req  Request
		want string
	}{
		{bashReq(cwd, "mkdir -p skip621"), Allow},
		{Request{ToolName: "write", ToolInput: map[string]any{"path": "a.go", "content": "x"}, Cwd: cwd}, Allow},
		{outside, Allow},
		{bashReq(cwd, "rm -rf build"), Deny},
		{bashReq(cwd, "git push origin x"), Ask},
		{bashReq(cwd, "gh pr merge 1 --squash"), Ask},
	} {
		if d := Decide(env, c.req); d.Decision != c.want {
			t.Errorf("%v under skip: %+v, want %s", c.req.ToolInput, d, c.want)
		}
	}
	env.SkipPermissions = false
	if d := Decide(env, bashReq(cwd, "mkdir -p skip621")); d.Decision != Ask {
		t.Errorf("without skip: %+v, want ask", d)
	}
}

// A call outside the sandbox asks once, here, saying so, even when an allow
// rule names the command, and offers no rule, since none could cover it.
func TestDecideAsksForACallOutsideTheSandbox(t *testing.T) {
	cwd := t.TempDir()
	env := Env{Rules: sources(t, cwd, map[string]map[string][]string{"user settings": {"allow": {"Bash(touch:*)"}}}),
		Grants: []grant.Grant{}}
	req := Request{ToolName: "bash", Cwd: cwd, ToolInput: map[string]any{"command": "touch x", "dangerouslyDisableSandbox": true}}
	d := Decide(env, req)
	if d.Decision != Ask || !strings.Contains(d.Reason, "outside the sandbox") || d.Suggestion != "" {
		t.Fatalf("unsandboxed touch with Bash(touch:*) allowed: %+v, want an outside-the-sandbox ask with no rule", d)
	}
	env.Grants = []grant.Grant{{Kind: grant.KindUnsandboxed, Value: "touch"}}
	if d := Decide(env, req); d.Decision != Allow {
		t.Errorf("with an unsandboxed_command grant: %+v, want allow", d)
	}
	if d := Decide(env, bashReq(cwd, "touch x")); d.Decision != Allow {
		t.Errorf("sandboxed touch: %+v, want allow by the rule", d)
	}
}

// A rule approved at this session's own dialog is an allow rule too: it
// covers the sandboxed command and not the same command outside the sandbox,
// which still asks, and draws the session's numbered dialog.
func TestADialogApprovalDoesNotCoverACallOutsideTheSandbox(t *testing.T) {
	cwd := t.TempDir()
	env := Env{Rules: sources(t, cwd, nil), Grants: []grant.Grant{}, Approvals: []string{"Bash(touch:*)"}}
	if d := Decide(env, bashReq(cwd, "touch x")); d.Decision != Allow {
		t.Fatalf("sandboxed touch: %+v, want allow by the dialog approval", d)
	}
	req := Request{ToolName: "bash", Cwd: cwd, ToolInput: map[string]any{"command": "touch x", "dangerouslyDisableSandbox": true}}
	d := Decide(env, req)
	if d.Decision != Ask || !strings.Contains(d.Reason, "outside the sandbox") || d.Suggestion != "" || d.Title == "" {
		t.Fatalf("unsandboxed touch under a dialog approval: %+v, want an outside-the-sandbox ask with a dialog and no rule", d)
	}
}

func TestHookReadsSkipPermissionsFromTheLaunch(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	in := `{"tool_name":"bash","tool_input":{"command":"mkdir -p skip621"},"cwd":"` + cwd + `"}`
	for value, want := range map[string]string{"1": Allow, "": Ask} {
		t.Setenv(envname.SkipPermissions, value)
		var d Decision
		if err := json.Unmarshal([]byte(Hook(strings.NewReader(in), t.TempDir(), "s1", "", time.Now())), &d); err != nil {
			t.Fatal(err)
		}
		if d.Decision != want {
			t.Errorf("%s=%q: %+v, want %s", envname.SkipPermissions, value, d, want)
		}
	}
}

// AskUserQuestion changes nothing, so it runs without a dialog, as in Claude
// Code, unless a deny or ask rule names it.
func TestDecideLetsAQuestionToTheUserThroughUnlessARuleNamesIt(t *testing.T) {
	cwd := t.TempDir()
	ask := Request{ToolName: "AskUserQuestion", ToolInput: map[string]any{"questions": []any{}}, Cwd: cwd}
	for list, want := range map[string]string{"": Allow, "deny": Deny, "ask": Ask} {
		lists := map[string][]string{}
		if list != "" {
			lists[list] = []string{"AskUserQuestion"}
		}
		env := Env{Rules: sources(t, cwd, map[string]map[string][]string{"user settings": lists}), Grants: []grant.Grant{}}
		if d := Decide(env, ask); d.Decision != want {
			t.Errorf("%q rule: %+v, want %s", list, d, want)
		}
	}
}

// Pi spells the board's mcp__gate-inbox__ tools mcp__gate_inbox__; a rule or
// grant in either spelling matches a call in either.
func TestDecideMatchesEitherSpellingOfAnMCPServer(t *testing.T) {
	cwd := t.TempDir()
	for _, call := range []string{"mcp__gate_inbox__send_session", "mcp__gate-inbox__send_session"} {
		req := Request{ToolName: call, ToolInput: map[string]any{}, Cwd: cwd}
		for _, rule := range []string{"mcp__gate-inbox__send_session", "mcp__gate_inbox__send_session", "mcp__gate-inbox"} {
			deny := Env{Rules: sources(t, cwd, map[string]map[string][]string{"user settings": {"deny": {rule}}}), Grants: []grant.Grant{}}
			if d := Decide(deny, req); d.Decision != Deny {
				t.Errorf("deny %s, call %s: %+v", rule, call, d)
			}
			allow := Env{Rules: sources(t, cwd, map[string]map[string][]string{"user settings": {"allow": {rule}}}), Grants: []grant.Grant{}}
			if d := Decide(allow, req); d.Decision != Allow {
				t.Errorf("allow %s, call %s: %+v", rule, call, d)
			}
		}
	}
	other := Request{ToolName: "mcp__gate_inbox__kill_session", ToolInput: map[string]any{}, Cwd: cwd}
	allowOne := Env{Rules: sources(t, cwd, map[string]map[string][]string{"user settings": {"allow": {"mcp__gate-inbox__send_session"}}}), Grants: []grant.Grant{}}
	if d := Decide(allowOne, other); d.Decision != Ask {
		t.Errorf("an allow rule for one tool allowed another: %+v", d)
	}
}

func TestDecideSuggestsARule(t *testing.T) {
	cwd := t.TempDir()
	env := Env{Rules: []Rules{}, Grants: []grant.Grant{}}
	for command, want := range map[string]string{
		"git push origin feature/x": "Bash(git push:*)",
		"make -j4":                  "Bash(make:*)",
		"go test ./...":             "Bash(go test:*)",
		"git push && rm x":          "",
	} {
		d := Decide(env, bashReq(cwd, command))
		if d.Suggestion != want {
			t.Errorf("%q suggests %q, want %q", command, d.Suggestion, want)
		}
	}
	d := Decide(env, Request{ToolName: "edit", ToolInput: map[string]any{"path": "a.go"}, Cwd: cwd})
	if d.Suggestion != "Edit(/"+filepath.Join(cwd, "a.go")+")" {
		t.Errorf("edit suggests %q", d.Suggestion)
	}
}

func TestDecideOffersNoRuleOnAnAskRuleDialog(t *testing.T) {
	cwd := t.TempDir()
	env := Env{Rules: sources(t, cwd, map[string]map[string][]string{
		"user settings": {"ask": {"Bash(echo rig-ask:*)"}},
	}), Grants: []grant.Grant{}, Approvals: []string{"Bash(echo:*)"}}
	d := Decide(env, bashReq(cwd, "echo rig-ask two"))
	if d.Decision != Ask || d.Suggestion != "" {
		t.Errorf("an ask-rule call = %+v, want an ask that offers no rule", d)
	}
}

func TestHookAsksOnAnUnreadablePayload(t *testing.T) {
	for _, in := range []string{"", "not json", `{"tool_input":{}}`} {
		var d Decision
		if err := json.Unmarshal([]byte(Hook(strings.NewReader(in), t.TempDir(), "s1", "", nowForTest)), &d); err != nil {
			t.Fatal(err)
		}
		if d.Decision != Ask {
			t.Errorf("%q: %+v", in, d)
		}
	}
}

func TestSourcesNamesEveryClaudeSettingsFile(t *testing.T) {
	got := Sources("/users/u/.claude", "/users/u", "/work/repo")
	var paths []string
	for _, s := range got {
		paths = append(paths, s.Path)
	}
	joined := strings.Join(paths, "\n")
	for _, want := range []string{"managed-settings.json", "/users/u/.claude/settings.json",
		"/work/repo/.claude/settings.json", "/work/repo/.claude/settings.local.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Sources lacks %s:\n%s", want, joined)
		}
	}
}

var nowForTest = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
