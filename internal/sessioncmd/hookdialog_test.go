package sessioncmd

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/hooks"
)

// A session whose hooks report a dialog is not at its prompt, however its
// pane reads: a command typed there would answer the dialog, and a queued
// message is held on it, which its sender is told.
func TestASessionWhoseHooksReportADialogIsNotAtItsPrompt(t *testing.T) {
	h := newSessionHarnessWithConfig(t, sessionConfig+hookedTool)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	sess := paneRunning(t, h, "cmd00040", "hooked", promptThenEcho, "❯")
	if err := h.store.SetAgentSessionID(sess.ID, "conv-dialog"); err != nil {
		t.Fatal(err)
	}
	(&transcript{}).ask("toolu_1", time.Now()).write(t, home, "conv-dialog")
	manager := hooks.NewManager(h.sessions.configDir)
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.StatusFile(sess.ID), []byte("waiting PreToolUse\nwaiting Notification\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := h.sessions.BoardCommand(context.Background(), sess.ID, "/model fast", "")
	if !errors.Is(err, extension.ErrNotAtPrompt) {
		t.Fatalf("BoardCommand err = %v, want ErrNotAtPrompt", err)
	}
	if pane := waitPane(t, h, sess.ID, "❯"); strings.Contains(pane, "ran:") {
		t.Fatalf("a refused command reached the pane:\n%s", pane)
	}

	runtime, err := h.sessions.open()
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.store.Close()
	held, err := runtime.heldReason(sess.ID)
	if err != nil || !strings.Contains(held, "sitting on a dialog") {
		t.Fatalf("heldReason = %q, %v; want the dialog named", held, err)
	}
}
