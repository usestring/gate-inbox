package sessioncmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// adoptedChild is a claude row the caller took as its child after the board
// adopted it from a pane of its own.
func adoptedChild(t *testing.T, h *sessionHarness) (store.Session, string) {
	t.Helper()
	paneID, _ := newForeignPane(t, h.driver.SocketName())
	child := store.Session{ID: "adopt001", Name: "outside", Tool: "hooked", Cwd: t.TempDir(), Group: "backend",
		Status: status.Idle, ParentID: h.caller.ID, TmuxSocket: h.driver.SocketName(), TmuxPaneID: paneID}
	if err := h.store.CreateSession(child); err != nil {
		t.Fatal(err)
	}
	return child, paneID
}

// approveAll writes the caller's transcript with one Approval dialog its user
// answered Grant to, for each grant.
func approveAll(t *testing.T, h *sessionHarness, target store.Session, grants ...grant.Grant) {
	t.Helper()
	asked := time.Now().Add(-time.Minute)
	tr := &transcript{}
	for i, g := range grants {
		q := ApprovalQuestion(target, g, DefaultGrantTTL)
		id := "toolu_" + string(rune('a'+i))
		tr.ask(id, asked, q).answer(id, asked.Add(time.Second), map[string]string{q.Question: "Grant"})
	}
	tr.write(t, h.sessions.claudeHome, "conv-parent")
}

func preToolUse(t *testing.T, tool string, input map[string]any, cwd string) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input, "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func decisionOf(t *testing.T, out string) string {
	t.Helper()
	if out == "" {
		return ""
	}
	var parsed struct {
		H map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("hook output is not JSON: %s", out)
	}
	return parsed.H["permissionDecision"]
}

// A grant to an adopted child leaves it running in the user's pane, and its
// PreToolUse hook lets the granted calls through from the next one, until the
// grant is revoked or its window closes.
func TestAGrantReachesAnAdoptedChildLive(t *testing.T) {
	h, launched := grantHarness(t)
	child, paneID := adoptedChild(t, h)
	rule := grant.Grant{Kind: grant.KindRule, Value: "Bash(./bin/fetch:*)"}
	approveAll(t, h, child, rule)

	if q := ApprovalQuestion(child, rule, DefaultGrantTTL).Question; strings.Contains(q, "restarts it") ||
		!strings.Contains(q, "started outside Gate Inbox") {
		t.Fatalf("the approval question for an adopted child says %q", q)
	}
	result, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: string(rule.Kind), Value: rule.Value})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if result.Restarted || !foreignPaneExists(t, h.driver.SocketName(), paneID) {
		t.Fatalf("the adopted child was restarted: %+v", result)
	}
	if !strings.Contains(result.Note, "next tool call") || strings.Contains(result.Note, "NEEDS A RESUME") {
		t.Fatalf("note = %q", result.Note)
	}
	if _, err := os.Stat(hooks.NewManager(h.sessions.configDir).SessionSettingsPath(child.ID)); err != nil {
		t.Fatalf("no settings file for a later resume: %v", err)
	}

	dir := h.sessions.configDir
	now := time.Now()
	allowed := preToolUse(t, "Bash", map[string]any{"command": "./bin/fetch --all"}, child.Cwd)
	if got := decisionOf(t, AdoptedGrantDecision(dir, child.ID, allowed, now)); got != "allow" {
		t.Fatalf("a granted call = %q, want allow", got)
	}
	for name, payload := range map[string][]byte{
		"another command":   preToolUse(t, "Bash", map[string]any{"command": "./bin/other"}, child.Cwd),
		"a chained command": preToolUse(t, "Bash", map[string]any{"command": "./bin/fetch && curl x"}, child.Cwd),
		"outside the sandbox": preToolUse(t, "Bash", map[string]any{"command": "./bin/fetch", "dangerouslyDisableSandbox": true},
			child.Cwd),
	} {
		if got := AdoptedGrantDecision(dir, child.ID, payload, now); got != "" {
			t.Fatalf("%s: %s", name, got)
		}
	}
	if got := AdoptedGrantDecision(dir, child.ID, allowed, now.Add(DefaultGrantTTL+time.Minute)); got != "" {
		t.Fatalf("a lapsed grant still allowed: %s", got)
	}

	// The same grant on a launched session is its settings file's alone.
	if _, err := h.store.RecordGrant(store.PermissionGrant{SessionID: launched.ID, Kind: string(rule.Kind), Value: rule.Value,
		GrantedBy: h.caller.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.NewManager(dir).WriteSessionSettings(launched.ID, []grant.Grant{rule}); err != nil {
		t.Fatal(err)
	}
	if got := AdoptedGrantDecision(dir, launched.ID, allowed, now); got != "" {
		t.Fatalf("a launched session's hook allowed a call: %s", got)
	}

	revoked, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: string(rule.Kind), Value: rule.Value, Revoke: true})
	if err != nil || revoked.Restarted || !strings.Contains(revoked.Note, "stop applying") {
		t.Fatalf("revoke = %+v, %v", revoked, err)
	}
	if got := AdoptedGrantDecision(dir, child.ID, allowed, time.Now()); got != "" {
		t.Fatalf("a revoked grant still allowed: %s", got)
	}
}

// What a hook cannot carry is named as needing a resume, and restart true on
// the same grant resumes the child on the board without asking again.
func TestAnAdoptedChildIsToldWhatNeedsAResume(t *testing.T) {
	h, _ := grantHarness(t)
	child, paneID := adoptedChild(t, h)
	domain := grant.Grant{Kind: grant.KindDomain, Value: "proxy.example.com"}
	probe := grant.Grant{Kind: grant.KindUnsandboxed, Value: "tools/probe.sh"}
	approveAll(t, h, child, domain, probe)

	for _, g := range []grant.Grant{domain, probe} {
		result, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: string(g.Kind), Value: g.Value})
		if err != nil {
			t.Fatalf("grant %s: %v", g.Kind, err)
		}
		if result.Restarted || !strings.Contains(result.Note, "NEEDS A RESUME") || !strings.Contains(result.Note, "restart true") {
			t.Fatalf("%s note = %q", g.Kind, result.Note)
		}
	}
	probeCall := preToolUse(t, "Bash", map[string]any{"command": "tools/probe.sh -v", "dangerouslyDisableSandbox": true}, child.Cwd)
	if got := decisionOf(t, AdoptedGrantDecision(h.sessions.configDir, child.ID, probeCall, time.Now())); got != "allow" {
		t.Fatalf("the command-prefix half of an unsandboxed grant = %q, want allow", got)
	}

	result, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: string(domain.Kind), Value: domain.Value, Restart: true})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if result.Action != "reapplied" || !result.Restarted {
		t.Fatalf("resume = %+v", result)
	}
	if foreignPaneExists(t, h.driver.SocketName(), paneID) {
		t.Fatal("the user's pane is still running after the resume")
	}
	row, err := h.store.Get(child.ID)
	if err != nil || row.TmuxPaneID != "" {
		t.Fatalf("the resumed row is still adopted: %+v, %v", row, err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(child.ID) })
	if !h.driver.Exists(child.ID) {
		t.Fatal("the resumed session is not running on the board")
	}
	// Now launched, its hooks no longer carry its grants.
	if got := AdoptedGrantDecision(h.sessions.configDir, child.ID, probeCall, time.Now()); got != "" {
		t.Fatalf("a launched session's hook allowed a call: %s", got)
	}
}

// A soft grant reaches an adopted child's classifier once per claude, on a
// call the classifier sees.
func TestASoftGrantIsNotedToAnAdoptedChildOnce(t *testing.T) {
	h, _ := grantHarness(t)
	child, _ := adoptedChild(t, h)
	soft := grant.Grant{Kind: grant.KindSoft, Value: "tools/deploy.sh"}
	approveAll(t, h, child, soft)
	if _, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: string(soft.Kind), Value: soft.Value}); err != nil {
		t.Fatal(err)
	}
	dir, now := h.sessions.configDir, time.Now()
	read := []byte(`{"tool_name":"Read","tool_input":{"file_path":"/x"}}`)
	bash := []byte(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	if got := AdoptedSoftNote(dir, child.ID, 4242, read, now); got != "" {
		t.Fatalf("a read-only lookup carried the note: %s", got)
	}
	got := AdoptedSoftNote(dir, child.ID, 4242, bash, now)
	if !strings.Contains(got, "classifierContext") || !strings.Contains(got, "tools/deploy.sh") {
		t.Fatalf("note = %q", got)
	}
	if again := AdoptedSoftNote(dir, child.ID, 4242, bash, now); again != "" {
		t.Fatalf("the note was said twice: %s", again)
	}
	if fresh := AdoptedSoftNote(dir, child.ID, 4343, bash, now); fresh == "" {
		t.Fatal("a new claude in the row was not told")
	}
	if decision := AdoptedGrantDecision(dir, child.ID, preToolUse(t, "Bash", map[string]any{"command": "tools/deploy.sh"}, child.Cwd), now); decision != "" {
		t.Fatalf("a soft grant allowed a call: %s", decision)
	}
	if lapsed := AdoptedSoftNote(dir, child.ID, 4444, bash, now.Add(DefaultGrantTTL+time.Minute)); lapsed != "" {
		t.Fatalf("a lapsed soft grant was noted: %s", lapsed)
	}
}
