package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
)

const deviceKeysFile = "[focus]\nhand_over = [\"§\"]\n\n" +
	"[device.\"ssh:203.0.113.7/xterm-256color\".focus]\nhand_over = [\"pgdown\"]\n"

// A device's table applies on that device only, over the shared ones, and the
// map follows the attached device when it changes.
func TestADeviceTableAppliesOnlyOnItsDevice(t *testing.T) {
	m := buildModel(t)
	m.themeDevice = "ssh:203.0.113.7/xterm-256color"
	writeKeys(t, m, deviceKeysFile)
	if len(m.keyProblems) > 0 {
		t.Fatalf("the file was refused: %v", m.keyProblems)
	}
	if got := m.km().Keys(keymap.ContextFocus, keymap.HandOver); len(got) != 1 || got[0] != "pgdown" {
		t.Fatalf("hand_over on the device is %v, want [pgdown]", got)
	}

	m.themeDevice = "local:ghostty"
	m.keysDeviceChanged()
	if got := m.km().Key(keymap.ContextFocus, keymap.HandOver); got != "§" {
		t.Fatalf("hand_over on another device is %q, want the shared §", got)
	}
	if _, ok := m.km().Action(keymap.ContextFocus, "pgdown"); ok {
		t.Fatal("pgdown is bound on a device whose table does not name it")
	}
}

// A device table's problem names the device, and leaves the shared binding.
func TestADeviceTableProblemNamesTheDevice(t *testing.T) {
	m := buildModel(t)
	m.themeDevice = "device:mac"
	writeKeys(t, m, "[device.\"device:mac\".focus]\nleave = []\n")
	if len(m.keyProblems) != 1 || !strings.HasPrefix(m.keyProblems[0], `device "device:mac": focus.leave`) {
		t.Fatalf("problems = %v", m.keyProblems)
	}
	if got := m.km().Key(keymap.ContextFocus, keymap.Leave); got != "ctrl+q" {
		t.Fatalf("leave fell to %q", got)
	}
}

// A rebind on a known device writes that device's table and leaves the shared
// tables and every other device's alone; resetting it falls back to shared.
func TestARebindOnADeviceWritesItsTable(t *testing.T) {
	m := buildModel(t)
	m.themeDevice = "device:mac"
	writeKeys(t, m, "[list]\narchive = [\"z\"]\n\n[device.\"device:other\".list]\narchive = [\"y\"]\n")
	if notes := m.rebind(keymap.ContextFocus, keymap.HandOver, []string{"pgdown"}); len(notes) > 0 {
		t.Fatalf("rebind refused: %v", notes)
	}
	file, err := keymap.LoadFile(m.configDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := file.Global[keymap.ContextList][keymap.Archive]; len(got) != 1 || got[0] != "z" {
		t.Fatalf("shared archive is %v", got)
	}
	if _, ok := file.Global[keymap.ContextFocus]; ok {
		t.Fatalf("the rebind leaked into the shared tables: %v", file.Global)
	}
	if got := file.Devices["device:mac"][keymap.ContextFocus][keymap.HandOver]; len(got) != 1 || got[0] != "pgdown" {
		t.Fatalf("device table is %v", file.Devices["device:mac"])
	}
	if got := file.Devices["device:other"][keymap.ContextList][keymap.Archive]; len(got) != 1 || got[0] != "y" {
		t.Fatalf("another device's table is %v", file.Devices["device:other"])
	}

	m.resetBinding(keymap.ContextFocus, keymap.HandOver)
	if got := m.km().Key(keymap.ContextFocus, keymap.HandOver); got != "§" {
		t.Fatalf("reset left hand_over on %q", got)
	}
	raw, err := os.ReadFile(keymap.Path(m.configDir()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "device:mac") {
		t.Fatalf("an emptied device table was kept:\n%s", raw)
	}
}

// The key map says which device's keys it is showing and editing.
func TestTheKeyMapNamesTheDevice(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 160, 50
	m.themeDevice = "device:mac"
	m.loadKeys()
	m.openHelp()
	if view := ansi.Strip(m.viewHelp()); !strings.Contains(view, "Keys · device:mac") {
		t.Fatalf("the key map does not name the device:\n%s", view)
	}
}
