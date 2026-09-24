package mcptool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension/mcptool"
)

func writeText(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The file form is a substitute for the inline text, never an addition to
// it, and it must be unambiguous about which file: an agent that names a
// relative path is told so rather than handed whatever sat in the server's
// working directory.
func TestTextArgTakesExactlyOneSource(t *testing.T) {
	path := writeText(t, "brief.md", "do the thing\n\n")

	if got, err := mcptool.TextArg("inline", "", "prompt", "prompt_file"); err != nil || got != "inline" {
		t.Fatalf("inline only = %q, %v", got, err)
	}
	if got, err := mcptool.TextArg("", path, "prompt", "prompt_file"); err != nil || got != "do the thing" {
		t.Fatalf("file only = %q, %v; want the file with its trailing newlines dropped", got, err)
	}
	if got, err := mcptool.TextArg("", "", "prompt", "prompt_file"); err != nil || got != "" {
		t.Fatalf("neither = %q, %v; want empty and no error, the tool decides whether that is allowed", got, err)
	}
	if _, err := mcptool.TextArg("inline", path, "prompt", "prompt_file"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("both: err = %v, want a refusal naming both", err)
	}
	if _, err := mcptool.TextArg("", "brief.md", "prompt", "prompt_file"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative: err = %v, want a refusal asking for an absolute path", err)
	}
	if _, err := mcptool.TextArg("", filepath.Join(t.TempDir(), "missing.md"), "prompt", "prompt_file"); err == nil || !strings.Contains(err.Error(), "prompt_file") {
		t.Fatalf("missing: err = %v, want it named by the argument", err)
	}
	empty := writeText(t, "empty.md", "\n")
	if _, err := mcptool.TextArg("", empty, "prompt", "prompt_file"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty file: err = %v, want a refusal", err)
	}
}

func TestTextArgExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "brief.md"), []byte("from home"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := mcptool.TextArg("", "~/brief.md", "prompt", "prompt_file"); err != nil || got != "from home" {
		t.Fatalf("~/brief.md = %q, %v", got, err)
	}
}

// A required field refuses a call that carried neither form, naming both.
func TestRequiredTextArgRefusesNeither(t *testing.T) {
	if _, err := mcptool.RequiredTextArg(" ", "", "message", "message_file"); err == nil || err.Error() != "pass message or message_file" {
		t.Fatalf("neither = %v", err)
	}
	if got, err := mcptool.RequiredTextArg("hi", "", "message", "message_file"); err != nil || got != "hi" {
		t.Fatalf("inline = %q, %v", got, err)
	}
}

func TestAnnotationsMarkAReadOnlyToolIdempotent(t *testing.T) {
	read := mcptool.Annotations(true, false, false)
	if !read.ReadOnlyHint || !read.IdempotentHint || *read.DestructiveHint || *read.OpenWorldHint {
		t.Fatalf("read-only = %+v", read)
	}
	write := mcptool.Annotations(false, true, true)
	if write.ReadOnlyHint || write.IdempotentHint || !*write.DestructiveHint || !*write.OpenWorldHint {
		t.Fatalf("destructive = %+v", write)
	}
}
