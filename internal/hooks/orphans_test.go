package hooks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneOrphansTakesOnlyOldFilesOfDeletedSessions(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager(dir)
	hooksDir := filepath.Dir(manager.StatusFile("x"))
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * OrphanGrace)
	write := func(name string, at time.Time) {
		t.Helper()
		path := filepath.Join(hooksDir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	gone := []string{"deadbeef.status.child-seg", "deadbeef.ask.json", "deadbeef.exit", "0123456789ab.conversation"}
	kept := []string{
		"cafef00d.status", "cafef00d.status.child-seg", "cafef00d.ask.json", // a live row
		"claude-settings-4cd571742030.json", "gate-inbox-mcp-claude.json", // shared files
		"abc.status",      // too short to be an id
		"DEADBEEF.status", // not lowercase hex
		"feedface",        // no suffix
	}
	for _, name := range gone {
		write(name, old)
	}
	for _, name := range kept {
		write(name, old)
	}
	write("0badf00d.status.child-seg", time.Now()) // deleted, but written within the grace
	kept = append(kept, "0badf00d.status.child-seg")
	if err := os.Mkdir(filepath.Join(hooksDir, "abcdef01.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	kept = append(kept, "abcdef01.d")

	removed, err := manager.PruneOrphans(map[string]bool{"cafef00d": true}, time.Now())
	if err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	if removed != len(gone) {
		t.Errorf("removed %d files, want %d", removed, len(gone))
	}
	for _, name := range gone {
		if _, err := os.Stat(filepath.Join(hooksDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep (err=%v)", name, err)
		}
	}
	for _, name := range kept {
		if _, err := os.Stat(filepath.Join(hooksDir, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}

func TestPruneOrphansWithoutADirectoryIsANoOp(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "missing"))
	removed, err := manager.PruneOrphans(nil, time.Now())
	if err != nil || removed != 0 {
		t.Fatalf("PruneOrphans = %d, %v; want 0, nil", removed, err)
	}
}
