package adopt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// An agent started from a shell is found by its tool's command, below the
// shell, and nothing else in the tree passes for it.
func TestProgramPIDFindsTheAgentBelowTheShell(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "someagent")
	pidFile := filepath.Join(dir, "pid")
	// A real agent CLI keeps its name in the tree, so this does not exec.
	script := "#!/bin/sh\necho $$ > '" + pidFile + "'\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	shell := exec.Command("/bin/sh", "-c", "'"+agent+"'; exit 0")
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("pkill", "-P", strconv.Itoa(shell.Process.Pid)).Run()
		_ = shell.Process.Kill()
		_ = shell.Wait()
	})
	var want int
	deadline := time.Now().Add(10 * time.Second)
	for want == 0 && time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			want, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if want == 0 {
		t.Fatal("the agent never started")
	}
	procs := NewProcTable()
	root := int32(shell.Process.Pid)
	if got, ok := procs.ProgramPID(root, "/usr/local/bin/someagent --resume"); !ok || got != want {
		t.Fatalf("ProgramPID = %d, %v; want the agent, %d", got, ok, want)
	}
	if got, ok := procs.ProgramPID(root, "otheragent"); ok {
		t.Fatalf("ProgramPID found otheragent at %d", got)
	}
	if got, ok := procs.ProgramPID(root, ""); ok {
		t.Fatalf("ProgramPID found an empty command at %d", got)
	}
}
