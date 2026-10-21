package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMergeGateConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMergeGateCheckRefusesWithoutAuthority(t *testing.T) {
	dir := writeMergeGateConfig(t, "poll_interval = \"2s\"\n")
	out := &bytes.Buffer{}
	err := RunMergeGateCheck(out, []string{"--command", "gh pr merge 331 --squash --delete-branch"}, dir)
	if err == nil || !strings.Contains(err.Error(), "no standing merge authority") {
		t.Fatalf("err = %v, want a refusal for missing authority", err)
	}
}

func TestMergeGateCheckRefusesShapesOutsideTheAuthority(t *testing.T) {
	dir := writeMergeGateConfig(t, "poll_interval = \"2s\"\n\n[merge_gate]\nowners = [\"usestring\"]\nauthors = [\"merge-author\"]\n")
	for _, command := range []string{
		"gh pr merge 331 --squash --delete-branch --admin",
		"echo hello",
	} {
		out := &bytes.Buffer{}
		err := RunMergeGateCheck(out, []string{"--command", command, "--cwd", t.TempDir()}, dir)
		if err == nil {
			t.Errorf("command %q admitted, want refuse", command)
		}
	}
}
