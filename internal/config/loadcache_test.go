package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, dir, text string, at time.Time) {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDirSeesAnEditWithTheSameSizeAndMtime(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeConfig(t, dir, "editor = \"vim\"\n", at)
	first, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, dir, "editor = \"nvi\"\n", at)
	second, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.Editor != "vim" || second.Editor != "nvi" {
		t.Fatalf("editor = %q then %q, want vim then nvi", first.Editor, second.Editor)
	}
}

func TestLoadDirFromTheCacheMatchesAFreshDecode(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "editor = \"vim\"\n[extensions.demo]\nlist = [\"a\", \"b\"]\nnested = { on = true }\n", time.Now())
	fresh, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fresh, cached) {
		t.Fatal("a cached load differs from the decode it was cached from")
	}
}

func TestLoadDirCallersCannotReachEachOthersConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "adopt_sockets = [\"one\"]\n[extensions.demo]\nlist = [\"a\"]\n", time.Now())
	first, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	first.AdoptSockets[0] = "changed"
	first.Extensions["demo"]["list"].([]any)[0] = "changed"
	for name := range first.Tools {
		delete(first.Tools, name)
	}
	second, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second.AdoptSockets[0] != "one" || second.Extensions["demo"]["list"].([]any)[0] != "a" || len(second.Tools) == 0 {
		t.Fatal("an edit to one caller's Config reached the next load")
	}
}

func TestLoadDirFollowsADefaultsChange(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "poll_interval = \"3s\"\n", time.Now())
	before, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := UseDefaults("editor = \"from-defaults\"\n")
	if err != nil {
		t.Fatal(err)
	}
	during, err := LoadDir(dir)
	restore()
	if err != nil {
		t.Fatal(err)
	}
	after, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before.Editor, "defaults") || during.Editor != "from-defaults" || after.Editor != before.Editor {
		t.Fatalf("editor = %q, %q, %q across UseDefaults and its restore", before.Editor, during.Editor, after.Editor)
	}
}
