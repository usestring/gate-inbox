package app

import "testing"

func TestCodexUtilitiesKeepNativeDispatch(t *testing.T) {
	for _, args := range [][]string{
		{"codex", "--version"}, {"codex", "exec", "task"}, {"codex", "-c", "model=exec", "mcp", "list"}, {"codex", "--model=resume", "app-server", "daemon", "version"},
	} {
		if !codexUtility(args) {
			t.Errorf("utility bound: %v", args)
		}
	}
	for _, args := range [][]string{
		{"codex"}, {"codex", "resume", "id"}, {"codex", "fork", "id"}, {"codex", "-m", "exec", "task"}, {"codex", "--", "exec"}, {"codex", "agents"},
	} {
		if codexUtility(args) {
			t.Errorf("interactive client unbound: %v", args)
		}
	}
}

func TestLegacyPrivateCodexNeedsNoNewFlags(t *testing.T) {
	if !legacyPrivateCodex("Codex CLI\nUsage: codex [OPTIONS] [PROMPT]\nOptions: --model --sandbox") {
		t.Fatal("old private CLI was forced through daemon flags")
	}
	for _, help := range []string{"Codex CLI --remote --no-daemon", "unrecognized output", "Codex CLI --remote"} {
		if legacyPrivateCodex(help) {
			t.Fatalf("daemon or unknown CLI bypassed binding: %q", help)
		}
	}
}
