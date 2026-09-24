package launch

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
)

// An extension's variables win over what the session would inherit, and an
// empty one withholds the inherited value: a variable that names the task a
// managed session works on must not leak into a session it spawns.
func TestComposeLetsContributedEnvOverrideAndWithholdInheritedVars(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat"}
	t.Setenv("GATE_INBOX_TEST_MARK", "the-parents-mark")
	t.Setenv("GATE_INBOX_TEST_LEAK", "the-parents-secret")
	_, env, err := Compose(manager, "plain", tool, tool.Command, "abcd1234", "", "", map[string]string{
		"GATE_INBOX_TEST_MARK": "this-sessions-mark",
		"GATE_INBOX_TEST_LEAK": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if env["GATE_INBOX_TEST_MARK"] != "this-sessions-mark" {
		t.Fatalf("contributed value lost to the inherited one: %v", env["GATE_INBOX_TEST_MARK"])
	}
	if value, set := env["GATE_INBOX_TEST_LEAK"]; !set || value != "" {
		t.Fatalf("an empty contribution must withhold the inherited value, got %q (set %v)", value, set)
	}
	if env[hooks.EnvSessionID] != "abcd1234" {
		t.Fatalf("session id = %q", env[hooks.EnvSessionID])
	}
}

func TestComposeRefusesContributedVarsTheLaunchSetsItself(t *testing.T) {
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "cat", AccountEnv: "TOOL_TOKEN", StatusSource: hooks.StatusSourceClaude}
	for _, key := range []string{hooks.EnvSessionID, hooks.EnvExecutable, config.HomeEnv, hooks.EnvStatusFile, "TOOL_TOKEN", "TMUX"} {
		_, _, err := Compose(manager, "plain", tool, tool.Command, "abcd1234", "", "", map[string]string{key: "x"})
		if err == nil || !strings.Contains(err.Error(), "the launch sets itself") {
			t.Errorf("contributing %s = %v, want it refused", key, err)
		}
	}
	if _, _, err := Compose(manager, "plain", tool, tool.Command, "abcd1234", "", "", map[string]string{"1BAD": "x"}); err == nil {
		t.Error("a name no shell can export was accepted")
	}
}
