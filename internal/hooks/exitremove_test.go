package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoveExitIfUnchangedKeepsAFresherRecord(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := os.MkdirAll(m.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	path := m.ExitFile("s")
	stale := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.WriteFile(path, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	_, at, ok := m.ReadExit("s")
	if !ok {
		t.Fatal("no record read")
	}

	// The agent exits between the read and the remove.
	if err := os.WriteFile(path, []byte("137\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stale.Add(time.Minute), stale.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveExitIfUnchanged("s", at); err != nil {
		t.Fatal(err)
	}
	if code, _, ok := m.ReadExit("s"); !ok || code != 137 {
		t.Fatalf("fresh exit = %d, %v; want 137 kept", code, ok)
	}

	_, at, _ = m.ReadExit("s")
	if err := m.RemoveExitIfUnchanged("s", at); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.ReadExit("s"); ok {
		t.Fatal("the inspected record survived")
	}
	if err := m.RemoveExitIfUnchanged("s", at); err != nil {
		t.Fatalf("missing record: %v", err)
	}
	leftovers, err := filepath.Glob(path + ".*")
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("held copies left behind: %v", leftovers)
	}
}
