package grant

import (
	"slices"
	"testing"
)

func TestMatchesAllowReadsSettingsRulesNarrowly(t *testing.T) {
	file := func(tool, path string) Call {
		return Call{Tool: tool, Input: map[string]any{"path": path}, Cwd: "/work/repo", Home: "/users/u"}
	}
	mcp := func(name string) Call { return Call{Tool: name, Input: map[string]any{}} }
	for _, c := range []struct {
		rule string
		call Call
		want bool
	}{
		{"Bash(git status)", bash("git status"), true},
		{"Bash(git push:*)", bash("git push origin x"), true},
		{"Bash(git push:*)", bash("git push origin x && rm -rf /"), false},
		{"Bash(git push:*)", bash("git pushx"), false},
		// A settings file may name a whole tool, which a grant may not.
		{"Bash", bash("anything at all"), true},
		{"Bash(*)", bash("anything at all"), true},
		{"Bash", Call{Tool: "Bash", Input: map[string]any{"command": "ls", "dangerouslyDisableSandbox": true}}, false},
		{"Bash(ls:*)", Call{Tool: "Bash", Input: map[string]any{"command": "ls", "dangerouslyDisableSandbox": true}}, false},
		{"Read", file("Grep", "/anywhere"), true},
		{"Edit", file("Write", "/anywhere"), true},
		{"Edit", file("Read", "/anywhere"), false},
		{"Read(./src/**)", file("Read", "src/a.go"), true},
		{"Read(./src/**)", file("Read", "docs/a.md"), false},
		{"mcp__gate-inbox", mcp("mcp__gate-inbox__report_status"), true},
		{"mcp__gate-inbox__*", mcp("mcp__gate-inbox__report_status"), true},
		{"mcp__gate-inbox", mcp("mcp__gate-inboxer__x"), false},
		{"mcp__gate-inbox__report_status", mcp("mcp__gate-inbox__report_status"), true},
		{"mcp__gate-inbox__report_status", mcp("mcp__gate-inbox__kill_session"), false},
		{"WebFetch(domain:example.com)", Call{Tool: "WebFetch", Input: map[string]any{"url": "https://example.com/a"}}, true},
		{"", bash("ls"), false},
		{"Bash(", bash("ls"), false},
	} {
		if got := MatchesAllow(c.rule, c.call); got != c.want {
			t.Errorf("MatchesAllow(%q, %s %v) = %v, want %v", c.rule, c.call.Tool, c.call.Input, got, c.want)
		}
	}
}

func TestMatchesDenyReadsSettingsRulesBroadly(t *testing.T) {
	for _, c := range []struct {
		rule string
		call Call
		want bool
	}{
		{"Bash(rm -rf:*)", bash("rm -rf build"), true},
		{"Bash(rm -rf:*)", bash("cd x && rm -rf y"), true},
		{"Bash(rm -rf:*)", bash("echo $(rm -rf y)"), true},
		{"Bash(rm -rf:*)", bash("FOO=1 rm -rf y"), true},
		{"Bash(rm -rf:*)", bash("ls | xargs echo; (rm -rf z)"), true},
		{"Bash(rm -rf:*)", bash("rm -r y"), false},
		{"Bash(git push --force:*)", bash("git push --force origin main"), true},
		{"Bash(git push --force:*)", bash("git push origin main"), false},
		{"Bash", bash("ls"), true},
		{"WebFetch", Call{Tool: "WebFetch", Input: map[string]any{"url": "https://x.test"}}, true},
		{"Read(~/.ssh/**)", Call{Tool: "Read", Input: map[string]any{"path": "~/.ssh/id_rsa"}, Home: "/users/u"}, true},
		{"Read(~/.ssh/**)", Call{Tool: "Read", Input: map[string]any{"path": "/users/u/notes"}, Home: "/users/u"}, false},
		// A /path rule is read from the root of the file it came from.
		{"Read(/secrets/**)", Call{Tool: "Read", Input: map[string]any{"path": "/work/repo/secrets/k"}, Cwd: "/work/repo", RuleBase: "/work/repo"}, true},
		{"Read(/secrets/**)", Call{Tool: "Read", Input: map[string]any{"path": "/work/repo/secrets/k"}, Cwd: "/work/repo"}, false},
	} {
		if got := MatchesDeny(c.rule, c.call); got != c.want {
			t.Errorf("MatchesDeny(%q, %s %v) = %v, want %v", c.rule, c.call.Tool, c.call.Input, got, c.want)
		}
	}
}

func TestCommandsSplitsEveryCommandALineRuns(t *testing.T) {
	got := Commands("A=1 B=2 make test && git push | tee x; echo `id` $(whoami)")
	want := []string{"make test", "git push", "tee x", "echo", "id", "whoami"}
	if !slices.Equal(got, want) {
		t.Fatalf("Commands = %q, want %q", got, want)
	}
}
