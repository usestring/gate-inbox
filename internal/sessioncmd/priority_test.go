package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/priority"
	"os"
)

// A declared tier lands in the mailbox the manager reads, spelled the way
// the store spells it, so the poller has nothing left to interpret.
func TestPriorityWritesTheTierForTheManager(t *testing.T) {
	configDir := t.TempDir()
	message, err := Priority(configDir, "cafe0001", " Urgent ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message, "urgent") {
		t.Fatalf("message does not say what was set: %q", message)
	}
	raw, err := os.ReadFile(hooks.NewManager(configDir).PriorityFile("cafe0001"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(priority.Urgent) {
		t.Fatalf("mailbox holds %q", raw)
	}
}

// Clearing is a tier like any other, and writes an empty mailbox rather
// than no mailbox: the poller has to be told, not left to time out.
func TestPriorityNoneClearsThroughTheMailbox(t *testing.T) {
	configDir := t.TempDir()
	if _, err := Priority(configDir, "cafe0001", "none"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(hooks.NewManager(configDir).PriorityFile("cafe0001"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("clearing wrote %q", raw)
	}
}

// A tier nobody has heard of is a message, not a silently ordinary
// session, and it never reaches the mailbox.
func TestPriorityRejectsAnUnknownTier(t *testing.T) {
	configDir := t.TempDir()
	_, err := Priority(configDir, "cafe0001", "p0")
	if err == nil {
		t.Fatal("p0 was accepted")
	}
	if !strings.Contains(err.Error(), "urgent") {
		t.Fatalf("error does not name the scale: %v", err)
	}
	if _, err := os.Stat(hooks.NewManager(configDir).PriorityFile("cafe0001")); !os.IsNotExist(err) {
		t.Fatalf("a rejected tier reached the mailbox: %v", err)
	}
}

// Outside a managed session there is no row to tier, and saying so names
// the variable rather than failing on a path.
func TestPriorityOutsideASessionSaysSo(t *testing.T) {
	if _, err := Priority(t.TempDir(), "", "high"); err == nil || !strings.Contains(err.Error(), hooks.EnvSessionID) {
		t.Fatalf("err = %v, want one naming %s", err, hooks.EnvSessionID)
	}
}
