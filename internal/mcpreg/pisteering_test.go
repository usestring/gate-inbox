package mcpreg

import (
	"os"
	"strings"
	"testing"
)

// A pi session hears the delegation and parent rules every launch carries,
// naming no Claude-only tool as its own, and the turn-end report_status rule
// the board reads; the naming ask rides along only when launches ask.
func TestPiSteering(t *testing.T) {
	for _, askRename := range []bool{false, true} {
		text := PiSteering(askRename)
		for _, want := range []string{"# Delegating work", parentSteeringHeading, "report_status", builtinDelegation["pi"]} {
			if !strings.Contains(text, want) {
				t.Fatalf("PiSteering(%v) lacks %q", askRename, want)
			}
		}
		if strings.Contains(text, "Agent tool") {
			t.Fatalf("PiSteering(%v) names Claude Code's subagent tool as pi's", askRename)
		}
		if got := strings.Contains(text, "# Session naming"); got != askRename {
			t.Fatalf("PiSteering(%v) carries the naming ask: %v", askRename, got)
		}
	}
}

// The own-server config is the claude one, written once under its stamped
// name, and a preview names that same file without writing it.
func TestOwnServerConfig(t *testing.T) {
	dir := t.TempDir()
	preview, err := OwnServerConfig("/bin/gate-inbox", dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(preview); !os.IsNotExist(err) {
		t.Fatalf("a preview wrote %s", preview)
	}
	path, err := OwnServerConfig("/bin/gate-inbox", dir, "", true)
	if err != nil || path != preview {
		t.Fatalf("OwnServerConfig = %s, %v; preview named %s", path, err, preview)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(claudeConfig("/bin/gate-inbox", "")) {
		t.Fatalf("written config differs (err %v):\n%s", err, data)
	}
}
