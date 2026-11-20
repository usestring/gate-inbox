package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A session's markers outlive Remove, which also runs for live sessions,
// and go once their status log has been gone a day.
func TestStatusSiblingsGoWithTheStatusLog(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(m.StatusFile("x")), 0o755); err != nil {
		t.Fatal(err)
	}
	touch := func(path string, age time.Duration) {
		t.Helper()
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(-age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}

	pinned := m.StatusFile("pinned")
	touch(pinned, 0)
	touch(pinned+ChildNudgeSuffix, 0)
	if err := m.Remove("pinned"); err != nil {
		t.Fatal(err)
	}
	if exists(pinned) || !exists(pinned+ChildNudgeSuffix) {
		t.Fatal("Remove should drop the log and keep what the live session was told")
	}

	live, orphan, fresh := m.StatusFile("live"), m.StatusFile("orphan"), m.StatusFile("fresh")
	touch(live, 0)
	touch(live+ChildSegmentStampSuffix, 48*time.Hour)
	touch(orphan+ChildSegmentStampSuffix, 48*time.Hour)
	touch(orphan+ChildNudgeSuffix, 48*time.Hour)
	touch(orphan+ParentRuleSuffix, 48*time.Hour)
	touch(fresh+ChildSegmentStampSuffix, time.Hour)
	if err := m.SweepStatusSiblings(time.Now()); err != nil {
		t.Fatal(err)
	}
	if exists(orphan+ChildSegmentStampSuffix) || exists(orphan+ChildNudgeSuffix) || exists(orphan+ParentRuleSuffix) {
		t.Fatal("the sweep kept an orphan's markers")
	}
	if !exists(live+ChildSegmentStampSuffix) || !exists(fresh+ChildSegmentStampSuffix) {
		t.Fatal("the sweep took a live session's marker or a fresh one")
	}
}
