package piext

import (
	"os"
	"path/filepath"
	"regexp"
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
