package sessioncmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// hookedTool stands in for Claude Code: a tool wired through the hooks
// settings file, which is what a grant is written into.
const hookedTool = `
[tools.hooked]
command = "cat"
revive_command = "cat"
default_status = "idle"
status_source = "claude-hooks"
`

func grantHarness(t *testing.T) (*sessionHarness, store.Session) {
	t.Helper()
	h := newSessionHarness(t)
	config := filepath.Join(h.sessions.configDir, "config.toml")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, append(raw, hookedTool...), 0o644); err != nil {
		t.Fatal(err)
	}
	h.sessions.claudeHome = t.TempDir()
	if err := h.store.SetAgentSessionID(h.caller.ID, "conv-parent"); err != nil {
		t.Fatal(err)
	}
	child := store.Session{ID: "child001", Name: "live-grading", Tool: "hooked", Cwd: t.TempDir(),
		Group: "backend", Status: status.Idle, ParentID: h.caller.ID, SpawnedBy: h.caller.ID}
	if err := h.driver.Create(child.ID, child.Cwd, "cat", nil, 80, 24); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(child.ID) })
	if err := h.store.CreateSession(child); err != nil {
		t.Fatal(err)
	}
	return h, child
}

func TestGrantNeedsTheUsersExactApproval(t *testing.T) {
	h, child := grantHarness(t)
	probe := grant.Grant{Kind: grant.KindUnsandboxed, Value: "tools/probe.sh"}
	req := GrantRequest{Kind: string(probe.Kind), Value: probe.Value}
	question := ApprovalQuestion(child, probe, DefaultGrantTTL)
	(&transcript{}).write(t, h.sessions.claudeHome, "conv-parent")

	_, err := h.sessions.Grant(h.caller.ID, child.ID, req)
	var need *ApprovalNeeded
	if !errors.As(err, &need) || need.Question.Question != question.Question ||
		!strings.Contains(err.Error(), `"header":"Approval"`) {
		t.Fatalf("unapproved grant err = %v, want ApprovalNeeded carrying the question", err)
	}

	asked := time.Now().Add(-time.Minute)
	(&transcript{}).ask("toolu_no", asked, question).
		answer("toolu_no", asked.Add(time.Second), map[string]string{question.Question: "Don't grant"}).
		write(t, h.sessions.claudeHome, "conv-parent")
	if _, err := h.sessions.Grant(h.caller.ID, child.ID, req); !errors.As(err, &need) ||
		!strings.Contains(err.Error(), `your user answered "Don't grant"`) {
		t.Fatalf("refused grant err = %v, want the user's refusal named", err)
	}

	paraphrase := question
	paraphrase.Question = "Can the child run the probe unsandboxed?"
	(&transcript{}).ask("toolu_para", asked, paraphrase).
		answer("toolu_para", asked.Add(time.Second), map[string]string{paraphrase.Question: "Grant"}).
		write(t, h.sessions.claudeHome, "conv-parent")
	if _, err := h.sessions.Grant(h.caller.ID, child.ID, req); !errors.As(err, &need) {
		t.Fatalf("paraphrased approval err = %v, want it refused", err)
	}

	(&transcript{}).ask("toolu_yes", asked, question).
		answer("toolu_yes", asked.Add(time.Second), map[string]string{question.Question: "Grant"}).
		write(t, h.sessions.claudeHome, "conv-parent")
	result, err := h.sessions.Grant(h.caller.ID, child.ID, req)
	if err != nil {
		t.Fatalf("approved grant: %v", err)
	}
	if result.Action != "granted" || !result.Restarted || len(result.Grants) != 1 {
		t.Fatalf("result = %+v, want one grant and a restart", result)
	}
	manager := hooks.NewManager(h.sessions.configDir)
	body, err := os.ReadFile(manager.SessionSettingsPath(child.ID))
	if err != nil {
		t.Fatalf("no session settings file: %v", err)
	}
	for _, want := range []string{`"Bash(tools/probe.sh:*)"`, `"excludedCommands"`, `"hooks"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("settings file lacks %s:\n%s", want, body)
		}
	}
	if _, err := os.Stat(filepath.Join(child.Cwd, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("the grant wrote into the child's directory: %v", err)
	}
	rows, err := h.store.Grants(child.ID, true)
	if err != nil || len(rows) != 1 || rows[0].EvidenceToolUseID != "toolu_yes" || rows[0].GrantedBy != h.caller.ID {
		t.Fatalf("ledger = %+v, %v", rows, err)
	}

	// The approval is spent: revoking and granting again needs a new one.
	if _, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: req.Kind, Value: req.Value, Revoke: true}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := os.Stat(manager.SessionSettingsPath(child.ID)); !os.IsNotExist(err) {
		t.Fatalf("revoking the only grant left the settings file: %v", err)
	}
	if _, err := h.sessions.Grant(h.caller.ID, child.ID, req); !errors.As(err, &need) ||
		!strings.Contains(err.Error(), "already settled a grant") {
		t.Fatalf("reused approval err = %v, want it refused as spent", err)
	}
	// One dialog asking two questions approves two grants, each once.
	domain := grant.Grant{Kind: grant.KindDomain, Value: "proxy.example.com"}
	rule := grant.Grant{Kind: grant.KindRule, Value: "Bash(./bin/fetch:*)"}
	both := []string{ApprovalQuestion(child, domain, DefaultGrantTTL).Question, ApprovalQuestion(child, rule, DefaultGrantTTL).Question}
	(&transcript{}).ask("toolu_two", asked, ApprovalQuestion(child, domain, DefaultGrantTTL), ApprovalQuestion(child, rule, DefaultGrantTTL)).
		answer("toolu_two", asked.Add(time.Second), map[string]string{both[0]: "Grant", both[1]: "Grant"}).
		write(t, h.sessions.claudeHome, "conv-parent")
	for _, g := range []grant.Grant{domain, rule} {
		if _, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: string(g.Kind), Value: g.Value}); err != nil {
			t.Fatalf("grant %s from a two-question dialog: %v", g.Kind, err)
		}
	}
	all, err := h.sessions.Grants(h.caller.ID, child.ID)
	if err != nil || len(all) != 3 || all[0].RevokedAt.IsZero() || !all[1].RevokedAt.IsZero() {
		t.Fatalf("grants = %+v, %v; want the first revoked and two in force", all, err)
	}
}

func TestGrantRefusesWhatNoApprovalCovers(t *testing.T) {
	h, child := grantHarness(t)
	_, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: "rule", Value: "Bash(*)"})
	var refusal *grant.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("Bash(*) err = %v, want a grant refusal", err)
	}
	if _, err := h.sessions.Grant(h.caller.ID, h.caller.ID, GrantRequest{Kind: "domain", Value: "example.com"}); err == nil {
		t.Fatal("a session granted itself a permission")
	}
	stranger := store.Session{ID: "stranger", Name: "stranger", Tool: "hooked", Cwd: t.TempDir(), Group: "backend",
		Status: status.Idle}
	if err := h.store.CreateSession(stranger); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.Grant(stranger.ID, child.ID, GrantRequest{Kind: "domain", Value: "example.com"}); err == nil {
		t.Fatal("a session granted a permission to a child it did not spawn")
	}
	echoer := childRunning(t, h, h.caller.ID, "child002", "echo-child", "❯ ", "cat %s; cat")
	if _, err := h.sessions.Grant(h.caller.ID, echoer.ID, GrantRequest{Kind: "domain", Value: "example.com"}); err == nil ||
		!strings.Contains(err.Error(), "cannot be granted a permission") {
		t.Fatalf("non-Claude child err = %v, want it refused", err)
	}
}

func TestAGrantLapsesWhenItsWindowCloses(t *testing.T) {
	h, child := grantHarness(t)
	soft := grant.Grant{Kind: grant.KindSoft, Value: "tools/probe.sh"}
	question := ApprovalQuestion(child, soft, 30*time.Minute)
	if !strings.Contains(question.Question, "for 30 minutes") {
		t.Fatalf("question %q does not name the window", question.Question)
	}
	asked := time.Now().Add(-time.Minute)
	(&transcript{}).ask("toolu_soft", asked, question).
		answer("toolu_soft", asked.Add(time.Second), map[string]string{question.Question: "Grant"}).
		write(t, h.sessions.claudeHome, "conv-parent")
	// An approval for one window does not cover another.
	if _, err := h.sessions.Grant(h.caller.ID, child.ID, GrantRequest{Kind: string(soft.Kind), Value: soft.Value}); err == nil {
		t.Fatal("an approval for 30 minutes granted the default window")
	}
	result, err := h.sessions.Grant(h.caller.ID, child.ID,
		GrantRequest{Kind: string(soft.Kind), Value: soft.Value, ExpiresIn: 30 * time.Minute})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if left := time.Until(result.Grant.ExpiresAt); left < 29*time.Minute || left > 31*time.Minute {
		t.Fatalf("grant expires in %s, want 30 minutes", left)
	}
	manager := hooks.NewManager(h.sessions.configDir)
	body, err := os.ReadFile(manager.SessionSettingsPath(child.ID))
	if err != nil || !strings.Contains(string(body), `"autoMode"`) || !strings.Contains(string(body), `"$defaults"`) {
		t.Fatalf("soft grant settings = %s, %v; want autoMode.allow keeping the defaults", body, err)
	}

	if n, err := h.sessions.ExpireGrants(time.Now()); err != nil || n != 0 {
		t.Fatalf("early sweep revoked %d, %v; want nothing", n, err)
	}
	if n, err := h.sessions.ExpireGrants(time.Now().Add(31 * time.Minute)); err != nil || n != 1 {
		t.Fatalf("sweep after the window revoked %d, %v; want 1", n, err)
	}
	if _, err := os.Stat(manager.SessionSettingsPath(child.ID)); !os.IsNotExist(err) {
		t.Fatalf("an expired grant left its settings file: %v", err)
	}
	all, err := h.sessions.Grants(h.caller.ID, child.ID)
	if err != nil || len(all) != 1 || all[0].RevokedBy != store.GrantExpired {
		t.Fatalf("grants = %+v, %v; want the one grant revoked as expired", all, err)
	}
}

func TestAWorkingSessionKeepsALapsedGrantUntilItRests(t *testing.T) {
	h, child := grantHarness(t)
	domain := grant.Grant{Kind: grant.KindDomain, Value: "proxy.example.com"}
	if _, err := h.store.RecordGrant(store.PermissionGrant{SessionID: child.ID, Kind: string(domain.Kind),
		Value: domain.Value, GrantedBy: h.caller.ID, ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpdateStatus(child.ID, status.Working); err != nil {
		t.Fatal(err)
	}
	if n, err := h.sessions.ExpireGrants(time.Now()); err != nil || n != 0 {
		t.Fatalf("sweep of a working session revoked %d, %v; want it left for its rest", n, err)
	}
	if err := h.store.UpdateStatus(child.ID, status.Idle); err != nil {
		t.Fatal(err)
	}
	if n, err := h.sessions.ExpireGrants(time.Now()); err != nil || n != 1 {
		t.Fatalf("sweep once it rested revoked %d, %v; want 1", n, err)
	}
}

func TestALapsedGrantOnADeletedSessionLeavesTheRecord(t *testing.T) {
	h, _ := grantHarness(t)
	if _, err := h.store.RecordGrant(store.PermissionGrant{SessionID: "gone0001", Kind: string(grant.KindDomain),
		Value: "proxy.example.com", GrantedBy: h.caller.ID, ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if n, err := h.sessions.ExpireGrants(time.Now()); err != nil || n != 1 {
		t.Fatalf("sweep revoked %d, %v; want the orphaned grant revoked without an error", n, err)
	}
}

// A merge the user approved is several commands, and asking for each in its
// own dialog made one decision four. One question names them all, one Grant
// covers them all, and asking again for a set that half landed asks only for
// what the child does not hold yet.
func TestOneApprovalGrantsEveryCommandItNames(t *testing.T) {
	h, child := grantHarness(t)
	steps := []string{"git commit -S -m pin", "git push origin HEAD", "gh pr merge 2544 --squash"}
	req := GrantRequest{Kind: string(grant.KindSoft), Value: steps[0], Values: steps[1:]}
	(&transcript{}).write(t, h.sessions.claudeHome, "conv-parent")

	_, err := h.sessions.Grant(h.caller.ID, child.ID, req)
	var need *ApprovalNeeded
	if !errors.As(err, &need) {
		t.Fatalf("unapproved err = %v, want ApprovalNeeded", err)
	}
	for _, step := range steps {
		if !strings.Contains(need.Question.Question, step) {
			t.Fatalf("question %q does not name %q", need.Question.Question, step)
		}
	}
	if !strings.Contains(need.Question.Question, "these 3 permissions") {
		t.Fatalf("question %q does not say how many it grants", need.Question.Question)
	}

	asked := time.Now().Add(-time.Minute)
	(&transcript{}).ask("toolu_all", asked, need.Question).
		answer("toolu_all", asked.Add(time.Second), map[string]string{need.Question.Question: "Grant"}).
		write(t, h.sessions.claudeHome, "conv-parent")
	result, err := h.sessions.Grant(h.caller.ID, child.ID, req)
	if err != nil {
		t.Fatalf("approved set: %v", err)
	}
	if len(result.Set) != 3 || !result.Restarted {
		t.Fatalf("result = %+v, want three granted and one restart", result)
	}
	rows, err := h.store.Grants(child.ID, true)
	if err != nil || len(rows) != 3 {
		t.Fatalf("in force = %+v, %v; want all three", rows, err)
	}
	if !strings.Contains(FormatGrant(result), "granted "+child.ID+" 3 permissions") {
		t.Fatalf("format = %q", FormatGrant(result))
	}

	more := GrantRequest{Kind: req.Kind, Value: steps[0], Values: []string{"gh pr checks 2544"}}
	if _, err := h.sessions.Grant(h.caller.ID, child.ID, more); !errors.As(err, &need) ||
		need.Question.Question != ApprovalQuestion(child, grant.Grant{Kind: grant.KindSoft, Value: "gh pr checks 2544"}, DefaultGrantTTL).Question {
		t.Fatalf("half-held err = %v, want a question for the one prefix not yet held", err)
	}

	chained := GrantRequest{Kind: req.Kind, Value: "git add go.mod", Values: []string{"git commit && git push"}}
	if _, err := h.sessions.Grant(h.caller.ID, child.ID, chained); err == nil || errors.As(err, &need) {
		t.Fatalf("chained err = %v, want the whole set refused before any question", err)
	}
}
