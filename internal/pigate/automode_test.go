package pigate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/jev"
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
		{bashReq(cwd, "rm -rf build"), false, Deny},
		{bashReq(cwd, "git push --force origin main"), false, Ask},
		{bashReq(cwd, "gh pr merge 1 --squash"), false, Ask},
		{Request{ToolName: "bash", Cwd: cwd, ToolInput: map[string]any{"command": "make", "dangerouslyDisableSandbox": true}}, false, Ask},
		{bashReq(cwd, "ls"), false, Allow},
	} {
		asked := 0
		env := Env{Rules: rules, Grants: []grant.Grant{}, Approvals: []string{},
			AutoMode: func(piauto.Request) piauto.Answer {
				asked++
				return piauto.Answer{Allow: true, Judged: true, InScope: 0.97, Safe: 0.95}
			}}
		d := Decide(env, c.req)
		if (asked == 1) != c.asked || d.Decision != c.want {
			t.Errorf("%v: %+v, asked %d", c.req.ToolInput, d, asked)
		}
		if c.asked && d.Reason != "auto-allowed by JEV (in_scope 0.97, safe 0.95)" {
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
	t.Setenv(jev.KeyEnv, "test-key")
	if err := os.MkdirAll(filepath.Dir(jev.KeyPath(configDir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jev.KeyPath(configDir), []byte("saved-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(bashReq(t.TempDir(), "go test ./..."))
	var d Decision
	if err := json.Unmarshal([]byte(Hook(strings.NewReader(string(payload)), configDir, "s1", "", nowForTest)), &d); err != nil {
		t.Fatal(err)
	}
	if d.Decision != Ask || !strings.Contains(d.Reason, "shared server is not answering") {
		t.Fatalf("%+v", d)
	}
}
