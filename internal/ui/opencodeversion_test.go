package ui

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/opencode"
)

// OpenCode v1 is not supported, so the startup notice is a warning: it lands
// on the status bar styled as a failure, once per run, and only for operators
// still on v1.
func TestOpencodeUpgradeNudge(t *testing.T) {
	v1 := &Model{}
	v1.noteOpencodeVersion(opencode.Report{Major: 1, Version: "1.18.30"})
	if !strings.Contains(v1.errBar.text, "not supported") {
		t.Fatalf("v1 operator: want the unsupported warning on the status bar, got %q", v1.errBar.text)
	}
	if v1.errBar.worked() {
		t.Errorf("warning %q reads as an outcome", v1.errBar.text)
	}
	if !strings.Contains(v1.errBar.text, "opencode.ai/v2/install") {
		t.Errorf("warning %q does not say how to upgrade", v1.errBar.text)
	}
	for name, report := range map[string]opencode.Report{
		"v2 operator":              {Major: 2, Version: "opencode v2.0.1", Schema: opencode.SchemaV2},
		"no opencode":              {},
		"v2 no store":              {Major: 2, Version: "opencode v2.0.1"},
		"unknown binary, v2 store": {Major: 0, Schema: opencode.SchemaV2},
	} {
		m := &Model{}
		m.noteOpencodeVersion(report)
		if m.errBar.text != "" {
			t.Errorf("%s: want silence, got %q", name, m.errBar.text)
		}
	}
}
