package sessioncmd

import (
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/store"
)

// A spawn under a high session has its window on disk before it launches,
// so its first run already compacts at the parent's window.
func TestSpawnCarriesItsParentsCompactWindow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateSession(store.Session{ID: "parent", Name: "parent", Tool: "claude", Cwd: dir, Priority: priority.High}); err != nil {
		t.Fatal(err)
	}
	manager := hooks.NewManager(dir)
	tool := config.Tool{StatusSource: hooks.StatusSourceClaude, CompactWindow: map[string]int{"high": 300000}}
	child := store.Session{ID: "child1", Tool: "claude", ParentID: "parent", SpawnedBy: "parent"}
	if err := writeSpawnCompactWindow(st, manager, tool, child); err != nil {
		t.Fatal(err)
	}
	if got, err := manager.SessionCompactWindow(child.ID); err != nil || got != 300000 {
		t.Fatalf("child window = %d, %v; want 300000", got, err)
	}
	if got := manager.LaunchSettingsPath(child.ID); got != manager.SessionSettingsPath(child.ID) {
		t.Fatalf("the child launches on %s, want its own file", got)
	}

	loose := store.Session{ID: "loose1", Tool: "claude", SpawnedBy: "parent"}
	if err := writeSpawnCompactWindow(st, manager, tool, loose); err != nil {
		t.Fatal(err)
	}
	if got := manager.LaunchSettingsPath(loose.ID); got != manager.SettingsPath() {
		t.Fatalf("a detached spawn launches on %s, want the shared file", got)
	}
}
