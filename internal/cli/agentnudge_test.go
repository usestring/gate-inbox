package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestHookAgentNudgeDeniesOnceAndOnlyOnTheBoard(t *testing.T) {
	configDir := tmuxtest.ScratchDir(t)
	t.Setenv("HOME", t.TempDir())
	write := `{"hook_event_name":"PreToolUse","tool_name":"Agent","cwd":"/work",` +
		`"tool_input":{"subagent_type":"general-purpose","description":"fix the build","prompt":"fix it"}}`
	read := `{"hook_event_name":"PreToolUse","tool_name":"Agent","cwd":"/work",` +
		`"tool_input":{"subagent_type":"Explore","description":"find it","prompt":"where"}}`
	run := func(payload string) string {
		var out bytes.Buffer
		if err := RunHook(strings.NewReader(payload), &out, []string{"agent-nudge"}, "abcd1234", configDir); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	t.Setenv(envname.StatusFile, "")
	if out := run(write); out != "" {
		t.Fatalf("with no status file the hook printed %q, want nothing", out)
	}

	t.Setenv(envname.StatusFile, filepath.Join(configDir, "abcd1234.status"))
	if out := run(read); out != "" {
		t.Fatalf("Explore got %q, want it to run", out)
	}
	out := run(write)
	if !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, `\"name\":\"fix-the-build\"`) {
		t.Fatalf("general-purpose got %q, want a deny carrying create_session", out)
	}
	if out := run(write); out != "" {
		t.Fatalf("the resent call got %q, want it to run", out)
	}
	if _, err := os.Stat(filepath.Join(configDir, "agent-nudge", "abcd1234.steered")); err != nil {
		t.Fatalf("no steered file in the state directory: %v", err)
	}
}
