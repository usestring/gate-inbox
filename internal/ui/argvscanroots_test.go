package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// Only a tool whose status comes from hooks can lose that wiring, so only its
// panes are worth reading argv under. The guard that matters is the first
// case: drop a claude pane here and the hookless mark stops appearing with no
// other symptom.
func TestArgvScanRootsPicksOnlyHooksBackedPanes(t *testing.T) {
	sessions := []store.Session{
		{ID: "claude-one", Tool: "claude"},
		{ID: "claude-two", Tool: "claude"},
		{ID: "a-codex", Tool: "codex"},
		{ID: "a-shell", Tool: "shell"},
		{ID: "archived-claude", Tool: "claude", Archived: true},
		{ID: "claude-no-pane", Tool: "claude"},
	}
	panes := map[string]int{
		"claude-one": 101, "claude-two": 102, "a-codex": 103,
		"a-shell": 104, "archived-claude": 105, "claude-no-pane": 0,
	}
	sources := map[string]string{"claude": hooks.StatusSourceClaude, "codex": "pane", "shell": "pane"}

	roots := argvScanRoots(sessions, panes, sources)

	for _, want := range []int{101, 102} {
		if !roots[want] {
			t.Errorf("pid %d is a live claude pane and was not scanned; the hookless mark needs it", want)
		}
	}
	for _, skip := range []int{103, 104, 105, 0} {
		if roots[skip] {
			t.Errorf("pid %d was scanned; it can carry no finding", skip)
		}
	}
	if len(roots) != 2 {
		t.Errorf("roots = %v, want exactly the two live claude panes", roots)
	}
}

// A board with no hooks-backed tool asks for no argv at all.
func TestArgvScanRootsIsNilWhenNothingQualifies(t *testing.T) {
	sessions := []store.Session{{ID: "a-codex", Tool: "codex"}}
	roots := argvScanRoots(sessions, map[string]int{"a-codex": 1}, map[string]string{"codex": "pane"})
	if roots != nil {
		t.Errorf("roots = %v, want nil so the sampler reads no cmdline at all", roots)
	}
}
