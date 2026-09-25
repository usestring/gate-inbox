package artifacts_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/extension/artifacts"
)

// A build that registers the extension owns [extensions.artifacts], so a
// config naming it configures; a build that does not ignores the section
// and reports it.
func TestTheSectionIsOwnedOnlyWhenTheBuildRegistersIt(t *testing.T) {
	sections := map[string]map[string]any{
		artifacts.Name: {"enabled": true, "base_url": "https://artifacts.example.test", "key_command": "sh"},
	}

	without, err := extension.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	// A build without the extension ignores its section, and says so.
	report := without.Configure(t.TempDir(), sections)
	if !slices.Contains(report.Unknown, artifacts.Name) || !strings.Contains(strings.Join(report.Notes(), "\n"), "[extensions."+artifacts.Name+"]: no extension in this build owns it") {
		t.Fatalf("a build without the extension did not report its section: %+v", report.Notes())
	}

	ext := artifacts.New()
	with, err := extension.NewRegistry([]extension.Extension{ext})
	if err != nil {
		t.Fatal(err)
	}
	if report := with.Configure(t.TempDir(), sections); !report.OK() {
		t.Fatalf("a build carrying the extension refused its section: %v", report.Notes())
	}
	if !ext.Enabled() {
		t.Fatal("the configured extension is off")
	}
}
