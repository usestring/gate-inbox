package keymap

import (
	"strings"
	"testing"
)

func TestDeviceLayersRoundTripThroughTheFile(t *testing.T) {
	want := File{
		Global:  Overrides{ContextFocus: {HandOver: {"§"}}},
		Devices: map[string]Overrides{"ssh:203.0.113.7/xterm": {ContextFocus: {HandOver: {"pgdown"}, Dismiss: {}}}},
	}
	dir := t.TempDir()
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if keys := got.Global[ContextFocus][HandOver]; len(keys) != 1 || keys[0] != "§" {
		t.Fatalf("shared hand_over came back as %v", keys)
	}
	layer := got.Devices["ssh:203.0.113.7/xterm"]
	if keys := layer[ContextFocus][HandOver]; len(keys) != 1 || keys[0] != "pgdown" {
		t.Fatalf("device hand_over came back as %v", keys)
	}
	if keys, ok := layer[ContextFocus][Dismiss]; !ok || len(keys) != 0 {
		t.Fatalf("an unbind in the device layer came back as %v, %v", keys, ok)
	}
	if text := EncodeFile(want); !strings.Contains(text, `[device."ssh:203.0.113.7/xterm".focus]`) {
		t.Fatalf("device table not written as expected:\n%s", text)
	}
}

// Precedence is default < shared < device, action by action.
func TestADeviceLayerWinsOverTheSharedTables(t *testing.T) {
	file := File{
		Global:  Overrides{ContextFocus: {HandOver: {"f9"}, Dismiss: {"alt+k"}}},
		Devices: map[string]Overrides{"device:mac": {ContextFocus: {HandOver: {"pgdown"}}}},
	}
	m := resolve(t, file.For("device:mac"))
	if got := m.Key(ContextFocus, HandOver); got != "pgdown" {
		t.Fatalf("hand_over = %q, want the device's pgdown", got)
	}
	if got := m.Key(ContextFocus, Dismiss); got != "alt+k" {
		t.Fatalf("dismiss = %q, want the shared alt+k", got)
	}
	if got := m.Key(ContextFocus, Leave); got != "ctrl+q" {
		t.Fatalf("leave = %q, want its default", got)
	}
	shared := resolve(t, file.For("device:other"))
	if got := shared.Key(ContextFocus, HandOver); got != "f9" {
		t.Fatalf("another device's hand_over = %q, want f9", got)
	}
	diff := m.Diff(shared)
	if len(diff) != 1 || len(diff[ContextFocus]) != 1 || diff[ContextFocus][HandOver][0] != "pgdown" {
		t.Fatalf("diff = %v", diff)
	}
}

func TestAMalformedDeviceTableIsAnError(t *testing.T) {
	if _, err := DecodeFile("[device.\"mac\".focus]\nleave = 3\n"); err == nil {
		t.Fatal("a device binding that is not a list of keys decoded")
	}
}
