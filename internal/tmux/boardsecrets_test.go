package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// A pane the board opens starts without the board's own credentials, though
// the board holds them and its tmux server copied them when it started: not
// in the agent, not in the shell that ran it, and not in the server's global
// environment for a show-environment or a later window to find. The launch's
// own variables still arrive, and the caller's env map is left as it was.
func TestAPaneStartsWithoutTheBoardsSecrets(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if runtime.GOOS != "linux" {
		t.Skip("reads the shell's environ from /proc")
	}
	for _, key := range envname.BoardSecrets {
		t.Setenv(key, "board-canary")
	}
	driver, err := NewWithSocket(tmuxtest.Socket(t, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(driver.CloseCaptureClients)
	out := filepath.Join(t.TempDir(), "out")
	command := "{ env; tr '\\0' '\\n' < /proc/$PPID/environ; } > " + ShellQuote(out+".part") + " && mv " + ShellQuote(out+".part") + " " + ShellQuote(out)
	env := map[string]string{"GI_TEST_KEPT": "kept"}
	id := "secrets" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	if err := driver.Create(id, t.TempDir(), command, env, 0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { driver.Kill(id) })
	var raw []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if raw, err = os.ReadFile(out); err == nil {
			break
		}
	}
	if err != nil {
		pane, _ := driver.CapturePane(id)
		t.Fatalf("the pane never wrote its environment: %v\n%s", err, pane)
	}
	for _, key := range envname.BoardSecrets {
		if strings.Contains(string(raw), key+"=") {
			t.Fatalf("%s reached the pane", key)
		}
		if global, _ := driver.run("show-environment", "-g"); strings.Contains(global, key+"=") {
			t.Fatalf("%s is still in the board server's global environment", key)
		}
		if _, set := env[key]; set {
			t.Fatalf("Create wrote %s into the caller's env map", key)
		}
	}
	if !strings.Contains(string(raw), "GI_TEST_KEPT=kept") {
		t.Fatalf("the launch's own variable was lost:\n%s", raw)
	}
}
