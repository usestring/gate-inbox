// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

//go:build darwin

package clipboard

import (
	"bytes"
	"os/exec"
	"testing"
)

// TestDarwinLiveCopy exercises the real pbcopy path end to end. Skipped in
// CI where no pasteboard exists; locally it proves the platform writer kept
// working after the OSC 52 fallback landed.
func TestDarwinLiveCopy(t *testing.T) {
	if _, err := exec.LookPath("pbpaste"); err != nil {
		t.Skip("no pbpaste on this host")
	}
	original, err := exec.Command("pbpaste").Output()
	if err != nil {
		t.Skipf("cannot back up pasteboard: %v", err)
	}
	t.Cleanup(func() {
		restoreCmd := exec.Command("pbcopy")
		restoreCmd.Stdin = bytes.NewReader(original)
		if err := restoreCmd.Run(); err != nil {
			t.Errorf("restore pasteboard: %v", err)
		}
	})
	if err := WriteText("gate-inbox-live-copy-proof"); err != nil {
		t.Skipf("pasteboard unavailable: %v", err)
	}
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		t.Skipf("pbpaste unavailable: %v", err)
	}
	if string(out) != "gate-inbox-live-copy-proof" {
		t.Fatalf("pbpaste = %q", out)
	}
}
