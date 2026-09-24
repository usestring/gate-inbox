package artifacts_test

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/extension/artifacts"
)

// A build that registers the extension owns [extensions.artifacts], so a
// config naming it configures; a build that does not refuses the section.
func TestTheSectionIsOwnedOnlyWhenTheBuildRegistersIt(t *testing.T) {
	sections := map[string]map[string]any{
		artifacts.Name: {"enabled": true, "base_url": "https://artifacts.example.test", "key_command": "sh"},
	}

	without, err := extension.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	err = without.Configure(t.TempDir(), sections)
	if err == nil || !strings.Contains(err.Error(), "no extension in this build owns: "+artifacts.Name) {
		t.Fatalf("a build without the extension accepted its section: %v", err)
	}

	ext := artifacts.New()
	with, err := extension.NewRegistry([]extension.Extension{ext})
	if err != nil {
		t.Fatal(err)
	}
	if err := with.Configure(t.TempDir(), sections); err != nil {
		t.Fatalf("a build carrying the extension refused its section: %v", err)
	}
	if !ext.Enabled() {
		t.Fatal("the configured extension is off")
	}
}
