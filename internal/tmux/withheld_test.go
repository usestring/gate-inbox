package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A withheld variable the tmux server holds reaches neither the agent nor
// the shell that runs it: a child can read its parent's environ, so an empty
// export over an inherited value would still leave it there to read.
func TestAWithheldVariableReachesNoProcessInThePane(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads the shell's environ from /proc")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	// The script's shell, and the shell tmux started it from.
	command := "{ env; echo ---; tr '\\0' '\\n' < /proc/$$/environ; tr '\\0' '\\n' < /proc/$PPID/environ; } > " + ShellQuote(out)
	env := map[string]string{"GI_TEST_KEY": Withheld, "GI_TEST_KEPT": "kept"}
	script, err := writeLaunchScript(filepath.Join(dir, "launch.sh"), env, command, "", launchShell{path: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	window := withheld(env) + launchShell{path: "/bin/true"}.runScript(script)
	// tmux hands the window command to its default shell, from an environment
	// fixed when the server started.
	cmd := exec.Command("/bin/sh", "-c", window)
	cmd.Env = append(os.Environ(), "GI_TEST_KEY=server-value")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", window, err, raw)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	agent, shell, _ := strings.Cut(string(raw), "---")
	if strings.Contains(agent, "GI_TEST_KEY") || strings.Contains(shell, "GI_TEST_KEY") {
		t.Fatalf("the withheld variable reached the pane:\n%s", raw)
	}
	if !strings.Contains(agent, "GI_TEST_KEPT=kept") {
		t.Fatalf("an ordinary variable was lost:\n%s", raw)
	}
}
