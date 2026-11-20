package pigate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/hooks"
)

func TestAnAskCarriesWhatTheDialogShows(t *testing.T) {
	cwd := t.TempDir()
	d := Decide(Env{Rules: []Rules{}, Grants: []grant.Grant{}, Approvals: []string{}}, bashReq(cwd, "git push origin feature/x"))
	if d.Decision != Ask || d.Title != "Bash command" || d.Block != "git push origin feature/x" {
		t.Fatalf("ask = %+v", d)
	}
	d = Decide(Env{Rules: []Rules{}, Grants: []grant.Grant{}, Approvals: []string{}},
		Request{ToolName: "mcp__x__delete", ToolInput: map[string]any{"id": 7}, Cwd: cwd})
	if d.Title != "Tool use" || d.Block != `mcp__x__delete {"id":7}` {
		t.Fatalf("mcp ask = %+v", d)
	}
}

func TestPendingRecordAndSessionApproval(t *testing.T) {
	dir := t.TempDir()
	status := filepath.Join(dir, "s1.status")
	cwd := t.TempDir()
	env := Env{Rules: []Rules{}, Grants: []grant.Grant{}, StatusFile: status}
	req := bashReq(cwd, "git push origin feature/x")
	d := Decide(env, req)
	if d.Decision != Ask || d.Suggestion != "Bash(git push:*)" {
		t.Fatalf("before = %+v", d)
	}

	PermissionPending(strings.NewReader(`{"tool":"Bash","title":"Bash command","block":"git push origin feature/x",`+
		`"ruleSuggestion":"Bash(git push:*)","tool_use_id":"call_1"}`), status)
	pending, ok := hooks.ReadPiPermissionFile(status + hooks.PiPermissionSuffix)
	if !ok || pending.Block != "git push origin feature/x" {
		t.Fatalf("pending = %+v, %v", pending, ok)
	}
	if raw, _ := os.ReadFile(status); !strings.Contains(string(raw), "waiting PermissionAsk") {
		t.Fatalf("status file = %q", raw)
	}

	// A grant for another call, or one naming its own rule, approves nothing.
	GrantSession(strings.NewReader(`{"tool_use_id":"call_2"}`), status)
	if d := Decide(env, req); d.Decision != Ask {
		t.Fatalf("another call's grant-session approved it: %+v", d)
	}
	GrantSession(strings.NewReader(`{"tool_use_id":"call_1","rule":"Bash(rm:*)"}`), status)
	if d := Decide(env, bashReq(cwd, "rm -r x")); d.Decision != Ask {
		t.Fatalf("a rule named in the payload was approved: %+v", d)
	}
	if d := Decide(env, req); d.Decision != Allow {
		t.Fatalf("after don't-ask-again = %+v", d)
	}
	PermissionResolved(status)
	if _, ok := hooks.ReadPiPermissionFile(status + hooks.PiPermissionSuffix); ok {
		t.Fatal("the pending record outlived its dialog")
	}
	// A deny rule still beats the approval.
	env.Rules = sources(t, cwd, map[string]map[string][]string{"user settings": {"deny": {"Bash(git push origin:*)"}}})
	if d := Decide(env, req); d.Decision != Deny {
		t.Fatalf("deny under an approval = %+v", d)
	}
}

func TestApprovalsFileIsCheckedAsGrants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")
	if err := os.WriteFile(path, []byte(`["Bash(*)","Bash(git push:*)","Read(~/.ssh/id_rsa)"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readApprovals(path); len(got) != 1 || got[0] != "Bash(git push:*)" {
		t.Fatalf("readApprovals = %q", got)
	}
}
