package piext

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// The file is named for its content, and Ensure writes exactly the bytes the
// name stands for.
func TestEnsureWritesTheStampedExtension(t *testing.T) {
	dir := t.TempDir()
	path, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if path != Path(dir) {
		t.Fatalf("Ensure wrote %s, Path says %s", path, Path(dir))
	}
	if !regexp.MustCompile(`^gate-inbox-pi-extension-[0-9a-f]{12}\.ts$`).MatchString(filepath.Base(path)) {
		t.Fatalf("name %q carries no content stamp", filepath.Base(path))
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(Source()) {
		t.Fatalf("written content differs (err %v)", err)
	}
	again, err := Ensure(dir)
	if err != nil || again != path {
		t.Fatalf("second Ensure = %s, %v", again, err)
	}
}

// omp loads the extension through a module with fewer exports than pi's, and
// one missing named import fails the whole file there, before any code runs.
// So values come from the agent module through its namespace only.
func TestTheExtensionImportsNoValueByNameFromPi(t *testing.T) {
	named := regexp.MustCompile(`(?s)import\s+(\{[^}]*\}|[A-Za-z_$][\w$]*\s*,\s*\{[^}]*\})\s+from\s+"@earendil-works/pi-coding-agent"`)
	if m := named.FindString(string(Source())); m != "" {
		t.Errorf("a value imported by name from pi's module:\n%s", m)
	}
	if !strings.Contains(string(Source()), `import * as piAgent from "@earendil-works/pi-coding-agent"`) {
		t.Error("the extension no longer reads pi's module through its namespace")
	}
}

// A build with a changed extension writes a file of its own beside the old
// one, and its launch names only its own.
func TestAChangedExtensionGetsANewFile(t *testing.T) {
	dir := t.TempDir()
	old, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := source
	t.Cleanup(func() { source = saved })
	source = append([]byte("// a later build\n"), saved...)
	current, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if current == old || Path(dir) != current {
		t.Fatalf("changed extension reused %s", old)
	}
	if Argv(current) == Argv(old) {
		t.Fatal("the launch argv did not move to the new file")
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("the old build's file went: %v", err)
	}
	if hooks.GeneratedName(fileName, source) != filepath.Base(current) {
		t.Fatal("name is not the content's")
	}
}
