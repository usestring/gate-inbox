package claudemcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(s Servers) []string {
	var out []string
	for name := range s {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func noEnv(string) (string, bool) { return "", false }

// The servers are the user's, the nearest project entry's (local scope) and
// an approved .mcp.json's, less the skipped and disabled ones; a name in
// several scopes takes local, then user, then project. The local scope is
// the repository root's entry, as Claude Code keys it.
func TestReadMergesClaudeCodeScopes(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(repo, "sub")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(home, ".claude.json"), `{
	  "mcpServers": {
	    "web": {"type": "http", "url": "https://user.test/mcp", "headers": {"Authorization": "Bearer user"}},
	    "gate-inbox": {"command": "gi"},
	    "off": {"command": "off"}
	  },
	  "projects": {
	    "`+repo+`": {
	      "mcpServers": {"localonly": {"command": "local"}},
	      "disabledMcpServers": ["off"],
	      "enabledMcpjsonServers": ["proj"]
	    }
	  }
	}`)
	write(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers": {
	  "web": {"type": "http", "url": "https://project.test/mcp"},
	  "proj": {"command": "proj"},
	  "unapproved": {"command": "nope"}
	}}`)
	got, err := Read(home, dir, []string{"gate-inbox"}, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"localonly", "proj", "web"}; !slices.Equal(names(got), want) {
		t.Fatalf("servers = %v, want %v", names(got), want)
	}
	var web struct{ URL string }
	_ = json.Unmarshal(got["web"], &web)
	if web.URL != "https://user.test/mcp" {
		t.Errorf("web came from %q, want the user entry", web.URL)
	}
}

// enableAllProjectMcpServers in the user's settings approves every server
// of a .mcp.json, and disabledMcpjsonServers still refuses one.
func TestReadHonoursProjectApproval(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"enableAllProjectMcpServers": true}`)
	write(t, filepath.Join(dir, ".claude", "settings.local.json"), `{"disabledMcpjsonServers": ["b"]}`)
	write(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers": {"a": {"command": "a"}, "b": {"command": "b"}}}`)
	got, err := Read(home, dir, nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a"}; !slices.Equal(names(got), want) {
		t.Fatalf("servers = %v, want %v", names(got), want)
	}
}

// No ~/.claude.json is no servers, not an error.
func TestReadWithoutAConfig(t *testing.T) {
	got, err := Read(t.TempDir(), t.TempDir(), nil, noEnv)
	if err != nil || len(got) != 0 {
		t.Fatalf("Read = %v, %v; want nothing", got, err)
	}
}

// ${VAR} and ${VAR:-default} expand as Claude Code expands them; an unset
// variable with no default stays as written.
func TestReadExpandsEnvironmentReferences(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers": {"s": {
	  "url": "https://${HOST}/mcp", "headers": {"X": "${MISSING:-fallback}", "Y": "${UNSET}"}
	}}}`)
	env := func(name string) (string, bool) {
		if name == "HOST" {
			return "example.test", true
		}
		return "", false
	}
	got, err := Read(home, t.TempDir(), nil, env)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		URL     string
		Headers map[string]string
	}
	_ = json.Unmarshal(got["s"], &s)
	if s.URL != "https://example.test/mcp" || s.Headers["X"] != "fallback" || s.Headers["Y"] != "${UNSET}" {
		t.Fatalf("expanded = %+v", s)
	}
}

// A linked worktree shares its main repository's projects entry; a submodule
// has its own.
func TestProjectKey(t *testing.T) {
	main := t.TempDir()
	write(t, filepath.Join(main, ".git", "HEAD"), "ref: refs/heads/main\n")
	gitDir := filepath.Join(main, ".git", "worktrees", "wt")
	write(t, filepath.Join(gitDir, "commondir"), "../..\n")
	wt := t.TempDir()
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gitDir+"\n")
	sub := filepath.Join(main, "sub")
	write(t, filepath.Join(sub, ".git"), "gitdir: ../.git/modules/sub\n")
	for dir, want := range map[string]string{
		filepath.Join(main, "a", "b"): main,
		filepath.Join(wt, "x"):        main,
		sub:                           sub,
	} {
		if got := ProjectKey(dir); got != want {
			t.Errorf("ProjectKey(%s) = %s, want %s", dir, got, want)
		}
	}
}
