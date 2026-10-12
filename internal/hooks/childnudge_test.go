package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The throttle lets one refresh a minute through to the binary and turns the
// rest away in the shell.
func TestThrottledChildDialogsCommandRunsOnceAMinute(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "gate-inbox")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" >> "+calls+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() {
		cmd := exec.Command("sh", "-c", ThrottledChildDialogsCommand())
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), EnvStatusFile + "=" + filepath.Join(dir, "s.status"),
			EnvExecutable + "=" + bin, EnvSessionID + "=a0a0a0a0"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v\n%s", err, out)
		}
	}
	run()
	run()
	raw, _ := os.ReadFile(calls)
	if got := strings.Count(string(raw), "hook child-dialogs"); got != 1 {
		t.Errorf("binary ran %d times in a minute, want 1:\n%s", got, raw)
	}
}
