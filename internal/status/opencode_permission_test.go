package status

import (
	"os"
	"path/filepath"
	"testing"
)

// The fixtures are verbatim "tmux capture-pane -p" output from a real
// opencode 1.18.23 driven into each state on a private tmux socket. They are
// kept unedited: the permission overlay replaces the input box, so the
// activity_cutoff marker is absent from the two dialog panes and present in
// the answered one, and that difference is what the rules turn on. OpenCode v2
// draws the same overlay text; the "▣" rows are 1.x chrome no rule reads.
func loadPane(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

// An opencode session held by a permission overlay must read waiting.
func TestOpencodePermissionOverlayReadsWaiting(t *testing.T) {
	engine := defaultEngine(t)
	for _, name := range []string{
		"opencode-permission-bash.txt",
		"opencode-permission-edit.txt",
	} {
		t.Run(name, func(t *testing.T) {
			if got, _ := engine.Match("opencode", loadPane(t, name)); got != Waiting {
				t.Fatalf("Match(opencode, %s) = %q want %q", name, got, Waiting)
			}
		})
	}
}

// Answering the overlay clears it from the pane entirely -- opencode draws it
// as an overlay, not as transcript text -- so the same session must stop
// reading waiting once the turn closes.
func TestOpencodeAnsweredPermissionDoesNotStayWaiting(t *testing.T) {
	engine := defaultEngine(t)
	if got, _ := engine.Match("opencode", loadPane(t, "opencode-v2-turn-finished.txt")); got == Waiting {
		t.Fatalf("Match(opencode, answered pane) = %q, want anything but %q", got, Waiting)
	}
}
