package grant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bash(command string) Call {
	return Call{Tool: "Bash", Input: map[string]any{"command": command}, Cwd: "/work/repo", Home: "/users/u"}
}

func TestAllowsReadsBashRulesNarrowly(t *testing.T) {
	for _, c := range []struct {
		grant Grant
		call  Call
		want  bool
	}{
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, bash("./bin/fetch"), true},
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, bash("./bin/fetch --all"), true},
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, bash("./bin/fetcher"), false},
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, bash("./bin/fetch && rm -rf /"), false},
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, bash("./bin/fetch; rm x"), false},
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, bash("./bin/fetch | sh"), false},
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, bash("./bin/fetch $(id)"), false},
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, bash("./bin/fetch\nrm x"), false},
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, bash("echo ./bin/fetch"), false},
		{Grant{KindRule, "Bash(bash ./scripts/:*)"}, bash("bash ./scripts/x.sh"), true},
		{Grant{KindRule, "Bash(npm run test)"}, bash("npm run test"), true},
		{Grant{KindRule, "Bash(npm run test)"}, bash("npm run test -- -u"), false},
		{Grant{KindRule, "Bash(npm run *)"}, bash("npm run lint"), true},
		{Grant{KindRule, "Bash(npm run *)"}, bash("npm install"), false},
		// A rule grant was not approved for leaving the sandbox.
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, Call{Tool: "Bash", Input: map[string]any{"command": "./bin/fetch", "dangerouslyDisableSandbox": true}}, false},
		// An unsandboxed-command grant was.
		{Grant{KindUnsandboxed, "tools/probe.sh"}, Call{Tool: "Bash", Input: map[string]any{"command": "tools/probe.sh -v", "dangerouslyDisableSandbox": true}}, true},
		{Grant{KindUnsandboxed, "tools/probe.sh"}, bash("tools/probe.sh"), true},
		{Grant{KindUnsandboxed, "tools/probe.sh"}, bash("tools/probe.shx"), false},
		// Neither a soft grant nor a domain allows a call.
		{Grant{KindSoft, "tools/probe.sh"}, bash("tools/probe.sh"), false},
		{Grant{KindDomain, "api.example.com"}, Call{Tool: "WebFetch", Input: map[string]any{"url": "https://api.example.com/x"}}, false},
		// A rule for another tool says nothing about Bash.
		{Grant{KindRule, "Read(//work/repo/**)"}, bash("cat /work/repo/x"), false},
		// A grant Check refuses allows nothing, whatever the ledger holds.
		{Grant{KindRule, "Bash(*)"}, bash("anything"), false},
		{Grant{KindRule, "Bash(cat ~/.ssh/id_rsa)"}, bash("cat ~/.ssh/id_rsa"), false},
		{Grant{KindRule, "Bash(cat ./gi/channel-keys/s1)"}, bash("cat ./gi/channel-keys/s1"), false},
	} {
		if got := Allows(c.grant, c.call); got != c.want {
			t.Errorf("Allows(%s %q, %v) = %v, want %v", c.grant.Kind, c.grant.Value, c.call.Input, got, c.want)
		}
	}
}

func TestAllowsReadsFileRulesNarrowly(t *testing.T) {
	file := func(tool, path string) Call {
		return Call{Tool: tool, Input: map[string]any{"file_path": path}, Cwd: "/work/repo", Home: "/users/u"}
	}
	search := func(tool, path string) Call {
		return Call{Tool: tool, Input: map[string]any{"path": path, "pattern": "x"}, Cwd: "/work/repo", Home: "/users/u"}
	}
	for _, c := range []struct {
		rule string
		call Call
		want bool
	}{
		{"Read(//data/shared/**)", file("Read", "/data/shared/a/b.txt"), true},
		{"Read(//data/shared/**)", file("Read", "/data/shared/../secret"), false},
		{"Read(//data/shared/**)", file("Read", "/data/sharedx/a"), false},
		{"Read(//data/shared/**)", search("Grep", "/data/shared/a"), true},
		{"Read(//data/shared/*.txt)", search("Grep", "/data/shared"), false},
		{"Read(//data/shared/*.txt)", file("Read", "/data/shared/a.txt"), true},
		{"Read(//data/shared/*.txt)", file("Read", "/data/shared/a/b.txt"), false},
		{"Read(~/notes/**)", file("Read", "/users/u/notes/x.md"), true},
		{"Read(~/notes/**)", file("Read", "~/notes/x.md"), true},
		{"Read(./docs/**)", file("Read", "docs/a.md"), true},
		{"Read(docs)", file("Read", "/work/repo/docs/a.md"), true},
		{"Read(./docs/**)", file("Read", "/work/other/docs/a.md"), false},
		{"Read(./docs/**)", file("Edit", "/work/repo/docs/a.md"), false},
		{"Edit(./docs/**)", file("Write", "/work/repo/docs/new.md"), true},
		{"Edit(./docs/**)", file("Read", "/work/repo/docs/new.md"), false},
		// Relative to a settings file the adopted session never loaded.
		{"Read(/docs/**)", file("Read", "/docs/a.md"), false},
		{"WebFetch(domain:api.example.com)", Call{Tool: "WebFetch", Input: map[string]any{"url": "https://api.example.com/v1"}}, true},
		{"WebFetch(domain:api.example.com)", Call{Tool: "WebFetch", Input: map[string]any{"url": "https://evil.api.example.com.x.io/"}}, false},
		{"Glob(//data/shared/**)", search("Glob", "/data/shared"), false},
	} {
		if got := Allows(Grant{KindRule, c.rule}, c.call); got != c.want {
			t.Errorf("Allows(%q, %s %v) = %v, want %v", c.rule, c.call.Tool, c.call.Input, got, c.want)
		}
	}
}

// A link inside an allowed directory does not carry the allow out of it.
func TestAllowsFollowsLinks(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	secret := filepath.Join(dir, "secret")
	for _, d := range []string{allowed, secret} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(secret, "s"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(secret, "s"), filepath.Join(allowed, "link")); err != nil {
		t.Fatal(err)
	}
	rule := Grant{KindRule, "Read(/" + allowed + "/**)"}
	if Allows(rule, Call{Tool: "Read", Input: map[string]any{"file_path": filepath.Join(allowed, "link")}}) {
		t.Fatal("a link out of the allowed directory was let through")
	}
}

func TestLiveSaysWhatWaitsForAResume(t *testing.T) {
	for _, c := range []struct {
		grant          Grant
		live, resume   bool
		resumeMentions string
	}{
		{Grant{KindRule, "Bash(./bin/fetch:*)"}, true, false, ""},
		{Grant{KindRule, "Read(./docs/**)"}, true, false, ""},
		{Grant{KindRule, "Read(/docs/**)"}, false, true, "Read(/docs/**)"},
		{Grant{KindRule, "WebSearch(query:x)"}, false, true, "WebSearch"},
		{Grant{KindUnsandboxed, "tools/probe.sh"}, true, true, "sandbox exclusion"},
		{Grant{KindDomain, "api.example.com"}, false, true, "api.example.com"},
		{Grant{KindSoft, "tools/probe.sh"}, true, false, ""},
	} {
		live, resume := Live(c.grant)
		if (live != "") != c.live || (resume != "") != c.resume || !strings.Contains(resume, c.resumeMentions) {
			t.Errorf("Live(%s %q) = %q, %q", c.grant.Kind, c.grant.Value, live, resume)
		}
	}
}
