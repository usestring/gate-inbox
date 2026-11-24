package sessioncmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostSnapshotReportsLiveWorkerSessions(t *testing.T) {
	h := newSessionHarness(t)
	snapshot, err := h.sessions.HostSessions()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Matched != 1 || len(snapshot.Sessions) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if row := snapshot.Sessions[0]; row.ID != h.caller.ID || row.Name != h.caller.Name || !row.Running {
		t.Fatalf("worker session = %+v, want live %s", row, h.caller.ID)
	}
	if err := h.sessions.AttachHost("unknown"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("attach unknown session = %v", err)
	}
	if err := h.driver.Kill(h.caller.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.sessions.AttachHost(h.caller.ID); err == nil || !strings.Contains(err.Error(), "no live pane") {
		t.Fatalf("attach stopped session = %v", err)
	}
}

func TestHostSnapshotRequiresAnExistingBoard(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewSessions(dir, CLIVocabulary()).HostSessions(); !os.IsNotExist(err) {
		t.Fatalf("snapshot of missing board = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("config must not be created by a snapshot: %v", err)
	}
}
