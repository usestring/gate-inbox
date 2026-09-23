// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A session's shell runs in a sandbox that cannot write the manager's
// config directory, and the bare "read-only file system" reads as the
// manager refusing: the error has to name the way that is open.
func TestRenameNamesTheSandboxWhenTheStateDirectoryRefusesTheWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through directory permissions")
	}
	configDir := tmuxtest.ScratchDir(t)
	hooksDir := filepath.Join(configDir, "hooks")
	if err := os.Mkdir(hooksDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(hooksDir, 0o755) })

	_, err := Rename(configDir, "cafe0001", "payments-fix")
	if err == nil {
		t.Fatal("rename into an unwritable directory succeeded")
	}
	if !strings.Contains(err.Error(), "Gate Inbox MCP tool") || !strings.Contains(err.Error(), "outside the sandbox") {
		t.Fatalf("error does not say where to go instead: %v", err)
	}
	if _, err := Rename(t.TempDir(), "cafe0001", "payments-fix"); err != nil {
		t.Fatalf("writable directory: %v", err)
	}
}
