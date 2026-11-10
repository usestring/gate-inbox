package pigate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

func TestToolNameMapsPiToolsToClaudeNames(t *testing.T) {
	for pi, want := range map[string]string{
		"bash": "Bash", "read": "Read", "write": "Write", "edit": "Edit",
		"grep": "Grep", "find": "Glob", "ls": "LS",
		"mcp__gate-inbox__report_status": "mcp__gate-inbox__report_status",
		"my_tool":                        "my_tool",
	} {
		if got := ToolName(pi); got != want {
			t.Errorf("ToolName(%q) = %q, want %q", pi, got, want)
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
	refused := func(string, string) (string, bool) { return "", false }
	if d := Decide(Env{Rules: rules, Grants: []grant.Grant{}, MergeGate: covered}, bashReq(cwd, "gh pr merge 1 --squash")); d.Decision != Allow {
		t.Errorf("covered merge: %+v", d)
	}
	if d := Decide(Env{Rules: rules, Grants: []grant.Grant{}, MergeGate: refused}, bashReq(cwd, "gh pr merge 1 --squash")); d.Decision != Ask {
		t.Errorf("refused merge: %+v", d)
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
		"ls":                        "",
	} {
		d := Decide(env, bashReq(cwd, command))
		if command == "ls" {
			continue // allowed on the fast path; no suggestion is read
		}
		if d.Suggestion != want {
			t.Errorf("%q suggests %q, want %q", command, d.Suggestion, want)
		}
	}
	d := Decide(env, Request{ToolName: "edit", ToolInput: map[string]any{"path": "a.go"}, Cwd: cwd})
	if d.Suggestion != "Edit(/"+filepath.Join(cwd, "a.go")+")" {
		t.Errorf("edit suggests %q", d.Suggestion)
	}
}

func TestHookAsksOnAnUnreadablePayload(t *testing.T) {
	for _, in := range []string{"", "not json", `{"tool_input":{}}`} {
		var d Decision
		if err := json.Unmarshal([]byte(Hook(strings.NewReader(in), t.TempDir(), "s1", nowForTest)), &d); err != nil {
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
