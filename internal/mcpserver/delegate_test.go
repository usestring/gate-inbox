package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/managerbuild"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func ptr[T any](v T) *T { return &v }

func TestSpawnArgsCarryEveryOptionTheToolTakes(t *testing.T) {
	args := spawnArgs(sessioncmd.CreateSessionOptions{
		Name: "reach-census", Prompt: "measure it", Tool: "claude", Model: "opus", Account: "ALICE1",
		Directory: "/repo", Group: ptr("research"), Nest: ptr(false),
	})
	got := strings.Join(args, " ")
	want := "spawn --json --name reach-census --prompt measure it --tool claude --model opus --account ALICE1 " +
		"--directory /repo --group research --nest=false"
	if got != want {
		t.Errorf("spawn args =\n%s\nwant\n%s", got, want)
	}
}

// An unset pointer means "not asked for": an omitted group inherits the
// caller's and an omitted worktree the group's default, so spelling either
// out on the command line would turn a default into an answer.
func TestSpawnArgsLeaveOutWhatWasNotAskedFor(t *testing.T) {
	got := strings.Join(spawnArgs(sessioncmd.CreateSessionOptions{Name: "loose"}), " ")
	if got != "spawn --json --name loose" {
		t.Errorf("spawn args = %s, want only the name", got)
	}
	// An explicit empty group IS an answer: it means the root group.
	got = strings.Join(spawnArgs(sessioncmd.CreateSessionOptions{Group: ptr("")}), " ")
	if got != "spawn --json --group " {
		t.Errorf("spawn args = %q, want an explicit empty group", got)
	}
}

func TestCreateSessionRunsInProcessWhenTheServerIsCurrent(t *testing.T) {
	dir := t.TempDir()
	if err := managerbuild.Record(dir); err != nil {
		t.Fatalf("record build: %v", err)
	}
	ran := false
	created, err := createSession(dir, "parent01", sessioncmd.CreateSessionOptions{Name: "child"},
		func(caller string, opts sessioncmd.CreateSessionOptions) (sessioncmd.Session, error) {
			ran = true
			if caller != "parent01" {
				t.Errorf("caller = %q, want parent01", caller)
			}
			return sessioncmd.Session{ID: "child001"}, nil
		})
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if !ran {
		t.Error("a current server delegated a spawn it could have run itself")
	}
	if created.ID != "child001" {
		t.Errorf("created = %+v, want the in-process row", created)
	}
}

// staleHome writes a build record naming a binary this process is not, so
// StaleSince reads this server as behind the board.
func staleHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	record := "0000000000000000000000000000000000000000000000000000000000000000 " +
		time.Now().Add(-2*time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(dir, managerbuild.FileName), []byte(record), 0o600); err != nil {
		t.Fatalf("write build record: %v", err)
	}
	if _, stale := managerbuild.StaleSince(dir, time.Now()); !stale {
		t.Fatalf("the fixture home does not read as stale")
	}
	return dir
}

// installFakeManager puts an executable at the path launch.Executable reads,
// so a delegated spawn runs it instead of the real manager. It writes its
// caller and its argv to argvFile and prints one session as JSON.
func installFakeManager(t *testing.T, home, argvFile string) {
	t.Helper()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("make bin: %v", err)
	}
	script := "#!/bin/sh\n" +
		"{ echo \"$GATE_INBOX_SESSION_ID\"; for a in \"$@\"; do echo \"$a\"; done; } > " + argvFile + "\n" +
		"echo '{\"id\":\"delegated1\",\"name\":\"reach-census\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "gate-inbox"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake manager: %v", err)
	}
}

// The whole point: a server too old to file a spawn under its caller runs it
// through the installed manager, as its caller, and takes that row back.
func TestCreateSessionDelegatesWhenTheServerIsStale(t *testing.T) {
	home := staleHome(t)
	argvFile := filepath.Join(t.TempDir(), "argv")
	installFakeManager(t, home, argvFile)
	t.Setenv(config.HomeEnv, home)

	created, err := createSession(home, "parent01", sessioncmd.CreateSessionOptions{
		Name: "reach-census", Tool: "claude", Account: "ALICE1", Nest: ptr(true),
	}, func(string, sessioncmd.CreateSessionOptions) (sessioncmd.Session, error) {
		t.Error("a stale server filed the row itself instead of delegating it")
		return sessioncmd.Session{ID: "flat"}, nil
	})
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if created.ID != "delegated1" {
		t.Errorf("created = %+v, want the delegate's row", created)
	}
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if lines[0] != "parent01" {
		t.Errorf("the delegate ran as %q, so the row would be filed under the wrong session", lines[0])
	}
	got := strings.Join(lines[1:], " ")
	if got != "spawn --json --name reach-census --tool claude --account ALICE1 --nest=true" {
		t.Errorf("delegate argv = %q", got)
	}
}

// A delegate that cannot run is a repair that did not happen, not a failed
// spawn: the row still has to be filed, flat if that is all this build can do.
func TestCreateSessionFallsBackWhenTheDelegateCannotRun(t *testing.T) {
	home := staleHome(t)
	// No bin/gate-inbox under it, and nothing on PATH to find instead.
	t.Setenv(config.HomeEnv, home)
	t.Setenv("PATH", t.TempDir())

	ran := false
	created, err := createSession(home, "parent01", sessioncmd.CreateSessionOptions{Name: "child"},
		func(string, sessioncmd.CreateSessionOptions) (sessioncmd.Session, error) {
			ran = true
			return sessioncmd.Session{ID: "child001"}, nil
		})
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if !ran {
		t.Error("a spawn was dropped because the delegate could not run it")
	}
	if created.ID != "child001" {
		t.Errorf("created = %+v, want the in-process row", created)
	}
}

// This process carries its own GATE_INBOX_SESSION_ID, and the delegate
// must run as the caller rather than as whichever of two entries the platform
// happens to hand back.
func TestWithSessionIDReplacesTheServersOwn(t *testing.T) {
	env := withSessionID([]string{"PATH=/bin", "GATE_INBOX_SESSION_ID=server01", "HOME=/root"}, "parent01")
	var found []string
	for _, entry := range env {
		if strings.HasPrefix(entry, "GATE_INBOX_SESSION_ID=") {
			found = append(found, entry)
		}
	}
	if len(found) != 1 || found[0] != "GATE_INBOX_SESSION_ID=parent01" {
		t.Errorf("session id entries = %v, want only the caller's", found)
	}
}

// The 2026-10-02 regression: a stale server revived a child in process, on
// its own build's hook settings, which had no prompt-submit hook, so every
// message its parent sent afterwards reached it with no verifying note. A
// stale server's revive runs on the installed manager, which writes today's.
func TestReviveSessionDelegatesWhenTheServerIsStale(t *testing.T) {
	home := staleHome(t)
	argvFile := filepath.Join(t.TempDir(), "argv")
	installFakeManager(t, home, argvFile)
	t.Setenv(config.HomeEnv, home)

	revived, err := reviveSession(home, "parent01", "child001",
		func(string, string) (sessioncmd.Session, error) {
			t.Error("a stale server revived the session on its own hook settings instead of delegating")
			return sessioncmd.Session{ID: "child001"}, nil
		})
	if err != nil {
		t.Fatalf("reviveSession failed: %v", err)
	}
	if revived.ID != "delegated1" {
		t.Errorf("revived = %+v, want the delegate's row", revived)
	}
	lines := delegateArgv(t, argvFile)
	if lines[0] != "parent01" {
		t.Errorf("the delegate ran as %q, want the caller", lines[0])
	}
	if got := strings.Join(lines[1:], " "); got != "revive --json -- child001" {
		t.Errorf("delegate argv = %q", got)
	}
}

func TestReviveSessionRunsInProcessWhenTheServerIsCurrent(t *testing.T) {
	dir := t.TempDir()
	if err := managerbuild.Record(dir); err != nil {
		t.Fatalf("record build: %v", err)
	}
	revived, err := reviveSession(dir, "parent01", "child001",
		func(caller, target string) (sessioncmd.Session, error) {
			if caller != "parent01" || target != "child001" {
				t.Errorf("in-process revive got (%q, %q)", caller, target)
			}
			return sessioncmd.Session{ID: target}, nil
		})
	if err != nil || revived.ID != "child001" {
		t.Errorf("reviveSession returned %+v, %v; want the in-process row", revived, err)
	}
}

func TestReviveSessionFallsBackWhenTheDelegateCannotRun(t *testing.T) {
	home := staleHome(t)
	t.Setenv(config.HomeEnv, home)
	t.Setenv("PATH", t.TempDir())

	revived, err := reviveSession(home, "parent01", "child001",
		func(_, target string) (sessioncmd.Session, error) { return sessioncmd.Session{ID: target}, nil })
	if err != nil || revived.ID != "child001" {
		t.Errorf("reviveSession returned %+v, %v; want the in-process row", revived, err)
	}
}

func TestMigrateSessionDelegatesWhenTheServerIsStale(t *testing.T) {
	home := staleHome(t)
	argvFile := filepath.Join(t.TempDir(), "argv")
	installFakeManager(t, home, argvFile)
	t.Setenv(config.HomeEnv, home)

	migrated, err := migrateSession(home, "parent01", "child001",
		sessioncmd.MigrateOptions{Tool: "codex", Name: "child-codex"},
		func(string, string, sessioncmd.MigrateOptions) (sessioncmd.Session, error) {
			t.Error("a stale server migrated the session on its own hook settings instead of delegating")
			return sessioncmd.Session{ID: "inproc"}, nil
		})
	if err != nil {
		t.Fatalf("migrateSession failed: %v", err)
	}
	if migrated.ID != "delegated1" {
		t.Errorf("migrated = %+v, want the delegate's row", migrated)
	}
	lines := delegateArgv(t, argvFile)
	if got := strings.Join(lines, " "); got != "parent01 migrate --json --tool codex --name child-codex -- child001" {
		t.Errorf("delegate caller and argv = %q", got)
	}
}

func TestMigrateArgsLeaveOutWhatWasNotAskedFor(t *testing.T) {
	if got := strings.Join(migrateArgs("child001", sessioncmd.MigrateOptions{}), " "); got != "migrate --json -- child001" {
		t.Errorf("migrate args = %q", got)
	}
}

func delegateArgv(t *testing.T, argvFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// The handler is wired through the delegate: a stale server's revive_session
// reaches the installed manager and never the in-process revive.
func TestReviveSessionToolDelegatesWhenTheServerIsStale(t *testing.T) {
	home := staleHome(t)
	argvFile := filepath.Join(t.TempDir(), "argv")
	installFakeManager(t, home, argvFile)
	t.Setenv(config.HomeEnv, home)
	fake := &fakeSessionCommands{created: sessioncmd.Session{ID: "child001"}}
	client := connectServer(t, newServer(home, "parent01", "test", &fakeTerminalCommands{}, fake))

	if text, isError := callText(t, client, "revive_session", map[string]any{"session_id": "child001"}); isError {
		t.Fatalf("revive_session failed: %s", text)
	}
	if fake.revivedID != "" {
		t.Error("a stale server revived the session in process")
	}
	if got := strings.Join(delegateArgv(t, argvFile), " "); got != "parent01 revive --json -- child001" {
		t.Errorf("delegate caller and argv = %q", got)
	}
}
