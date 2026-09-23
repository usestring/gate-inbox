package sessioncmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// modelsConfig writes a config holding only the tools a case needs, since
// LoadDir backfills the built-in ones around them and those shell out to
// CLIs that may not be installed on the machine running the test.
func modelsConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestModelsRunsTheToolsOwnCommand(t *testing.T) {
	dir := modelsConfig(t, `
[tools.echoer]
command = "echoer"
model_flag = "--model"
models_command = "printf 'alpha\nbeta\n'"
`)
	out, err := Models(dir, "echoer", "")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	for _, want := range []string{"alpha", "beta", "printf"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Models = %q, want it to contain %q", out, want)
		}
	}
}

func TestModelsFallsBackToConfiguredNames(t *testing.T) {
	dir := modelsConfig(t, `
[tools.echoer]
command = "echoer"
model_flag = "--model"
models = ["sonnet", "opus"]
`)
	out, err := Models(dir, "echoer", "")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if !strings.Contains(out, "sonnet") || !strings.Contains(out, "opus") {
		t.Fatalf("Models = %q, want the configured names", out)
	}
	if !strings.Contains(out, "may lag") {
		t.Fatalf("Models = %q, want it to say the written list may lag the CLI", out)
	}
}

// A failing listing command is reported as that failure. Swallowing it would
// read as "this CLI has no models", and the caller would guess a name for a
// CLI that was perfectly able to answer.
func TestModelsReportsACommandThatFails(t *testing.T) {
	dir := modelsConfig(t, `
[tools.echoer]
command = "echoer"
model_flag = "--model"
models_command = "echo nope >&2; exit 1"
`)
	out, err := Models(dir, "echoer", "")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if !strings.Contains(out, "failed") || !strings.Contains(out, "nope") {
		t.Fatalf("Models = %q, want the failure and its stderr", out)
	}
}

// A tool that cannot be launched on a model at all says so here, where the
// caller is choosing, rather than at spawn time where it is a dead session.
func TestModelsSaysWhenAToolHasNoModelFlag(t *testing.T) {
	dir := modelsConfig(t, `
[tools.echoer]
command = "echoer"
models = ["ignored"]
`)
	out, err := Models(dir, "echoer", "")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if !strings.Contains(out, "no model flag") {
		t.Fatalf("Models = %q, want it to name the missing model flag", out)
	}
}

func TestModelsRejectsAnUnknownTool(t *testing.T) {
	dir := modelsConfig(t, `
[tools.echoer]
command = "echoer"
model_flag = "--model"
models = ["sonnet"]
`)
	if _, err := Models(dir, "nosuchtool", ""); err == nil {
		t.Fatal("an unknown tool was accepted")
	} else if !strings.Contains(err.Error(), "echoer") {
		t.Fatalf("error = %v, want it to list the configured tools", err)
	}
}

// The cap exists so one CLI's several hundred models cannot crowd out the
// caller's own context, and the filter is how they get past it.
func TestModelsCapsAndFilters(t *testing.T) {
	dir := modelsConfig(t, `
[tools.echoer]
command = "echoer"
model_flag = "--model"
models_command = "seq 1 100 | sed 's/^/muse-/'"
`)
	out, err := Models(dir, "echoer", "")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	// Counted from line starts, since the header echoes the command that
	// generated the names and holds "muse-" itself.
	if lines := strings.Count(out, "\nmuse-"); lines > modelsCap {
		t.Fatalf("Models listed %d names, want at most the %d cap", lines, modelsCap)
	}
	if !strings.Contains(out, "and 60 more") {
		t.Fatalf("Models = %q, want the count it left out", out)
	}
	narrowed, err := Models(dir, "echoer", "muse-7")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	// muse-7, muse-70..muse-79: eleven, and no cap note.
	if got := strings.Count(narrowed, "muse-7"); got != 11 {
		t.Fatalf("filtered Models kept %d names, want 11: %q", got, narrowed)
	}
	if strings.Contains(narrowed, "more; pass filter") {
		t.Fatalf("filtered Models = %q, want no cap note", narrowed)
	}
}

func TestModelsReportsAFilterThatMatchesNothing(t *testing.T) {
	dir := modelsConfig(t, `
[tools.echoer]
command = "echoer"
model_flag = "--model"
models = ["sonnet", "opus"]
`)
	out, err := Models(dir, "echoer", "gpt")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if !strings.Contains(out, "no model matching") {
		t.Fatalf("Models = %q, want it to say nothing matched", out)
	}
}

// The whole point is the real CLI's real answer, which the fixtures above
// cannot reach. Runs where opencode is installed and skips where it is not,
// starting from an empty directory so LoadDir writes the built-in defaults
// and the models_command under test is the shipped one.
func TestModelsAsksTheInstalledOpencode(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode is not installed")
	}
	out, err := Models(t.TempDir(), "opencode", "muse-spark")
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if !strings.Contains(out, "muse-spark") {
		t.Fatalf("Models = %q, want the muse-spark names opencode lists", out)
	}
}
