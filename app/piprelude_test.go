package app

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/mcpshared"
	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/piext"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

const piAutoModeTokenChildEnv = "GATE_INBOX_TEST_PI_AUTO_MODE_TOKEN_DIR"

// A pi pane's first step writes the session's token, then runs pi in its
// place, so the token is there before pi (and any confinement around it)
// starts. The step runs in a child process, since it execs.
func TestPiAutoModeTokenWritesTheTokenBeforeTheCommandRuns(t *testing.T) {
	if dir := os.Getenv(piAutoModeTokenChildEnv); dir != "" {
		path := mcpshared.ClaudeConfigPath(dir, "abcd1234")
		err := runPiPrelude(launch.AutoModeTokenVerb, []string{"abcd1234", "--", "/bin/sh", "-c", `test -s "$0" && echo token-before-pi`, path}, dir)
		t.Fatalf("the step did not exec its command: %v", err)
	}
	dir := t.TempDir()
	listener, lock, err := mcpshared.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proof, _ := parentseal.ServerProof(dir, r.URL.Query().Get("nonce"))
		_, _ = w.Write([]byte(proof))
	})}
	go server.Serve(listener)
	defer server.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestPiAutoModeTokenWritesTheTokenBeforeTheCommandRuns$", "-test.count=1")
	cmd.Env = append(tmuxtest.Environ(), piAutoModeTokenChildEnv+"="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "token-before-pi" {
		t.Fatalf("pane step = %q, %v; want the token written before the command ran", out, err)
	}
}

// A malformed call is refused rather than running anything.
func TestPiAutoModeTokenRefusesAMalformedCall(t *testing.T) {
	for _, args := range [][]string{nil, {"abcd1234"}, {"abcd1234", "pi"}, {"abcd1234", "--"}} {
		if err := runPiPrelude(launch.AutoModeTokenVerb, args, t.TempDir()); err == nil {
			t.Errorf("args %q ran", args)
		}
	}
}

const piAutoModeSecretsChildEnv = "GATE_INBOX_TEST_PI_AUTO_MODE_SECRETS_DIR"

// The verb runs as gate-inbox, whose build may fill in defaults for the
// board's own secrets on startup; pi, which the verb becomes, gets none of
// them.
func TestPiAutoModeTokenKeepsTheBoardsSecretsFromPi(t *testing.T) {
	if dir := os.Getenv(piAutoModeSecretsChildEnv); dir != "" {
		err := runPiPrelude(launch.AutoModeTokenVerb, []string{"abcd1234", "--", "/bin/sh", "-c", `env | grep -E '^(TYPESAFE_API_KEY|GATE_INBOX_TRACE_SECRET)=' ; echo pi-ran`}, dir)
		t.Fatalf("the step did not exec its command: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPiAutoModeTokenKeepsTheBoardsSecretsFromPi$", "-test.count=1")
	cmd.Env = append(tmuxtest.Environ(), piAutoModeSecretsChildEnv+"="+t.TempDir(),
		envname.TypeSafeKey+"=operator-key", envname.TraceSecret+"=SOME_SECRET_NAME")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "pi-ran" {
		t.Fatalf("pi saw %q, %v; want none of the board's secrets", out, err)
	}
}

const piPreludeClaudeMCPChildEnv = "GATE_INBOX_TEST_PI_PRELUDE_CLAUDE_MCP_DIR"

// With --claude-mcp the pane's first step writes the servers a Claude Code
// session in the pane's directory would use, before pi runs.
func TestPiPreludeWritesTheClaudeMCPServersBeforeTheCommandRuns(t *testing.T) {
	if dir := os.Getenv(piPreludeClaudeMCPChildEnv); dir != "" {
		path := piext.ClaudeMCPConfig(parentseal.KeyDir(dir), "abcd1234")
		err := runPiPrelude(launch.PreludeVerb, []string{"abcd1234", launch.PreludeClaudeMCP, "--", "/bin/sh", "-c", `grep -q '"web"' "$0" && echo servers-before-pi`, path}, dir)
		t.Fatalf("the step did not exec its command: %v", err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"web":{"type":"http","url":"https://example.test/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPiPreludeWritesTheClaudeMCPServersBeforeTheCommandRuns$", "-test.count=1")
	cmd.Env = append(tmuxtest.Environ(), piPreludeClaudeMCPChildEnv+"="+t.TempDir(), "HOME="+home)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "servers-before-pi" {
		t.Fatalf("pane step = %q, %v; want the servers written before the command ran", out, err)
	}
}

// An unknown flag is refused rather than running anything.
func TestPiPreludeRefusesAnUnknownFlag(t *testing.T) {
	if err := runPiPrelude(launch.PreludeVerb, []string{"abcd1234", "--bogus", "--", "true"}, t.TempDir()); err == nil {
		t.Fatal("an unknown flag ran")
	}
}
