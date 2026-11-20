package pigate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/classifier"
	"github.com/usestring/gate-inbox/internal/mcpshared"
	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/piauto"
)

func TestAutoModeIsAskedOnlyAboutWhatNothingElseSettled(t *testing.T) {
	cwd := t.TempDir()
	rules := sources(t, cwd, map[string]map[string][]string{"user settings": {
		"deny": {"Bash(rm -rf:*)"},
		"ask":  {"Bash(git push --force:*)"},
	}})
	for _, c := range []struct {
		req   Request
		asked bool
		want  string
	}{
		{bashReq(cwd, "go test ./..."), true, Allow},
		{bashReq(cwd, "gofmt -w main.go"), true, Allow},
		{Request{ToolName: "write", Cwd: cwd, ToolInput: map[string]any{"path": "main.go", "content": "package main"}}, true, Allow},
		{Request{ToolName: "mcp__example__lookup", Cwd: cwd, ToolInput: map[string]any{"query": "docs"}}, true, Allow},
		{bashReq(cwd, "rm -rf build"), false, Deny},
		{bashReq(cwd, "git push --force origin main"), false, Ask},
		{bashReq(cwd, "gh pr merge 1 --squash"), false, Ask},
		{Request{ToolName: "bash", Cwd: cwd, ToolInput: map[string]any{"command": "make", "dangerouslyDisableSandbox": true}}, false, Ask},
		{bashReq(cwd, "ls"), false, Allow},
	} {
		asked := 0
		env := Env{Rules: rules, Grants: []grant.Grant{}, Approvals: []string{},
			AutoMode: func(req piauto.Request) piauto.Answer {
				asked++
				c := piauto.Classifier{Ask: func(context.Context, string, any, map[string]string) (map[string]float64, bool) {
					return map[string]float64{"in_scope": 0.50, "safe": 0.98}, true
				}}
				return c.Judge("s1", "key", req)
			}}
		d := Decide(env, c.req)
		if (asked == 1) != c.asked || d.Decision != c.want {
			t.Errorf("%v: %+v, asked %d", c.req.ToolInput, d, asked)
		}
		if c.asked && d.Reason != "auto-allowed by JEV (in_scope 0.50, safe 0.98)" {
			t.Errorf("reason %q", d.Reason)
		}
	}
}

func TestAutoModeDecliningAsksWithItsScores(t *testing.T) {
	cwd := t.TempDir()
	env := Env{Rules: []Rules{}, Grants: []grant.Grant{}, Approvals: []string{},
		AutoMode: func(piauto.Request) piauto.Answer {
			return piauto.Answer{Judged: true, InScope: 0.97, Safe: 0.41, Note: "JEV in_scope 0.97, safe 0.41 → asking"}
		}}
	d := Decide(env, bashReq(cwd, "git push origin main"))
	if d.Decision != Ask || !strings.Contains(d.Reason, "JEV in_scope 0.97, safe 0.41 → asking") {
		t.Fatalf("%+v", d)
	}
}

// The hook never reads the TypeSafe key: with a key in the environment and
// saved, and no board server to ask, every undecided call still asks.
func TestHookWithAKeyButNoBoardServerAsks(t *testing.T) {
	configDir := t.TempDir()
	// The operator's own settings could allow the call before auto mode is
	// reached.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv(classifier.JEV.KeyEnv(), "test-key")
	if err := os.MkdirAll(filepath.Dir(classifier.JEV.KeyPath(configDir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(classifier.JEV.KeyPath(configDir), []byte("saved-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(bashReq(t.TempDir(), "go test ./..."))
	var d Decision
	if err := json.Unmarshal([]byte(Hook(strings.NewReader(string(payload)), configDir, "s1", "", nowForTest)), &d); err != nil {
		t.Fatal(err)
	}
	if d.Decision != Ask || !strings.Contains(d.Reason, "pi auto mode is unavailable") {
		t.Fatalf("%+v", d)
	}
}

// Under confinement (package keyconfine) the hook cannot read the caller key,
// only the session's own MCP config. It proves itself with the token there,
// checks the server with a proof keyed by that token, and so still reaches
// the classifier; a server that cannot give that proof is never sent it.
func TestHookAsksAutoModeWithOnlyItsOwnConfigReadable(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	token, err := parentseal.CallerToken(configDir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	board := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case mcpshared.HealthPath:
			_, _ = w.Write([]byte(parentseal.SessionServerProof(token, r.URL.Query().Get("nonce"))))
		case piauto.Path:
			asked = append(asked, r.Header.Get(mcpshared.CallerTokenHeader))
			_ = json.NewEncoder(w).Encode(piauto.Answer{Allow: true, Judged: true, InScope: 0.97, Safe: 0.96})
		}
	}))
	defer board.Close()
	if err := os.WriteFile(filepath.Join(configDir, "mcp-serve.url"), []byte(board.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpshared.WriteClaudeConfig(configDir, board.URL, "", "s1"); err != nil {
		t.Fatal(err)
	}
	// What confinement hides.
	if err := os.Remove(filepath.Join(parentseal.KeyDir(configDir), "caller.key")); err != nil {
		t.Fatal(err)
	}
	decide := func() Decision {
		payload, _ := json.Marshal(bashReq(t.TempDir(), "go version"))
		var d Decision
		if err := json.Unmarshal([]byte(Hook(strings.NewReader(string(payload)), configDir, "s1", "", nowForTest)), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := decide(); d.Decision != Allow || !strings.Contains(d.Reason, "auto-allowed by JEV") || len(asked) != 1 || asked[0] != token {
		t.Fatalf("confined hook: %+v, classifier asked with %q", d, asked)
	}

	squatter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == piauto.Path {
			asked = append(asked, r.Header.Get(mcpshared.CallerTokenHeader))
		}
	}))
	defer squatter.Close()
	if err := os.WriteFile(filepath.Join(configDir, "mcp-serve.url"), []byte(squatter.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := decide(); d.Decision != Ask || !strings.Contains(d.Reason, "not answering") || len(asked) != 1 {
		t.Fatalf("squatter: %+v, tokens sent %q", d, asked)
	}
}
