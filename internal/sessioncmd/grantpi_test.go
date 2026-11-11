package sessioncmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/pigate"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const piHookedTool = `
[tools.pihooked]
command = "cat"
revive_command = "cat"
default_status = "idle"
status_source = "pi-hooks"
`

// piGrantHarness is grantHarness with a pi child.
func piGrantHarness(t *testing.T) (*sessionHarness, store.Session) {
	t.Helper()
	h, _ := grantHarness(t)
	config := filepath.Join(h.sessions.configDir, "config.toml")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, append(raw, piHookedTool...), 0o644); err != nil {
		t.Fatal(err)
	}
	child := store.Session{ID: "pichild1", Name: "pi-worker", Tool: "pihooked", Cwd: t.TempDir(),
		Group: "backend", Status: status.Working, ParentID: h.caller.ID, SpawnedBy: h.caller.ID}
	if err := h.driver.Create(child.ID, child.Cwd, "cat", nil, 80, 24); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(child.ID) })
	if err := h.store.CreateSession(child); err != nil {
		t.Fatal(err)
	}
	return h, child
}

// The decision pi's tool gate makes for a push from the child.
func piPush(h *sessionHarness, child store.Session) string {
	return pigate.Decide(pigate.Env{ConfigDir: h.sessions.configDir, SessionID: child.ID, Rules: []pigate.Rules{}},
		pigate.Request{ToolName: "bash", ToolInput: map[string]any{"command": "git push origin feature/x"}, Cwd: child.Cwd}).Decision
}

func TestAGrantReachesAPiChildLiveWithoutARestart(t *testing.T) {
	h, child := piGrantHarness(t)
	push := grant.Grant{Kind: grant.KindRule, Value: "Bash(git push origin:*)"}
	req := GrantRequest{ExpiresIn: DefaultGrantTTL, Kind: string(push.Kind), Value: push.Value}
	if got := piPush(h, child); got != pigate.Ask {
		t.Fatalf("before the grant: %s", got)
	}
	question := ApprovalQuestion(child, push, DefaultGrantTTL)
	asked := time.Now().Add(-time.Minute)
	(&transcript{}).ask("toolu_pi", asked, question).
		answer("toolu_pi", asked.Add(time.Second), map[string]string{question.Question: "Grant"}).
		write(t, h.sessions.claudeHome, "conv-parent")
	result, err := h.sessions.Grant(h.caller.ID, child.ID, req)
	if err != nil {
		t.Fatalf("approved grant: %v", err)
	}
	if result.Action != "granted" || result.Restarted || !strings.Contains(result.Note, "next call without a restart") {
		t.Fatalf("result = %+v, want a live grant", result)
	}
	if _, err := os.Stat(hooks.NewManager(h.sessions.configDir).SessionSettingsPath(child.ID)); err != nil {
		t.Fatalf("no session settings file for the gate to see: %v", err)
	}
	if got := piPush(h, child); got != pigate.Allow {
		t.Fatalf("after the grant: %s", got)
	}

	if _, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: req.Kind, Value: req.Value, Revoke: true}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := piPush(h, child); got != pigate.Ask {
		t.Fatalf("after the revoke: %s", got)
	}
}

func TestAPiChildIsRefusedTheKindsItsGateCannotUse(t *testing.T) {
	h, child := piGrantHarness(t)
	for kind, says := range map[string]string{
		"domain":       "no sandbox network",
		"soft_command": "not told of approved prefixes",
	} {
		value := "example.com"
		if kind == "soft_command" {
			value = "tools/probe.sh"
		}
		_, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{ExpiresIn: DefaultGrantTTL, Kind: kind, Value: value})
		if err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: err = %v, want it to say %q", kind, err, says)
		}
	}
}

func TestAnExpiredGrantLeavesAWorkingPiChildAtOnce(t *testing.T) {
	h, child := piGrantHarness(t)
	now := time.Now()
	if _, err := h.store.RecordGrant(store.PermissionGrant{SessionID: child.ID, Kind: "rule", Value: "Bash(git push origin:*)",
		GrantedBy: h.caller.ID, EvidenceToolUseID: "toolu_x", QuestionHash: "h", CreatedAt: now.Add(-time.Hour),
		ExpiresAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	revoked, err := h.sessions.ExpireGrants(now)
	if err != nil || revoked != 1 {
		t.Fatalf("revoked %d, err %v; a working pi child keeps nothing past its window", revoked, err)
	}
}
