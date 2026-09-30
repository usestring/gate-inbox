package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// A server the driver starts in a test must not source the operator's
// ~/.tmux.conf: their plugins would run on every one of them. The driver finds
// tmux on PATH, so it gets tmuxtest's shim like everything else.
func TestTheDriverStartsServersWithoutTheUserConfig(t *testing.T) {
	driver := requireTmux(t)
	if driver.bin != tmuxtest.ShimPath() {
		t.Fatalf("the driver runs %s, want the shim %s", driver.bin, tmuxtest.ShimPath())
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("set -g @gitest-user-config loaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	socket := tmuxtest.Socket(t, "userconfig")
	if out, err := driver.combined([]string{"-L", socket, "new-session", "-d", "sleep 60"}); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	out, err := driver.combined([]string{"-L", socket, "show-options", "-gqv", "@gitest-user-config"})
	if err != nil {
		t.Fatalf("show-options: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Fatalf("a server the driver started sourced the user's tmux config: @gitest-user-config = %q", got)
	}
}
