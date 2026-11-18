package app

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/mcpshared"
	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

const piAutoModeTokenChildEnv = "GATE_INBOX_TEST_PI_AUTO_MODE_TOKEN_DIR"

// A pi pane's first step writes the session's token, then runs pi in its
// place, so the token is there before pi (and any confinement around it)
// starts. The step runs in a child process, since it execs.
func TestPiAutoModeTokenWritesTheTokenBeforeTheCommandRuns(t *testing.T) {
	if dir := os.Getenv(piAutoModeTokenChildEnv); dir != "" {
		path := mcpshared.ClaudeConfigPath(dir, "abcd1234")
		err := runPiAutoModeToken([]string{"abcd1234", "--", "/bin/sh", "-c", `test -s "$0" && echo token-before-pi`, path}, dir)
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
		if err := runPiAutoModeToken(args, t.TempDir()); err == nil {
			t.Errorf("args %q ran", args)
		}
	}
}
