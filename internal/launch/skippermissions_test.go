package launch

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

// The shipped defaults are the feature for the two CLIs that have a flag: a
// session launched with skip permissions on must actually lose its prompts,
// on a fresh launch and on the resume a revive runs.
func TestSkipPermissionsPutsEachCLIsOwnFlagOnLaunchAndRevive(t *testing.T) {
	cfg, err := config.LoadDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	for name, flag := range map[string]string{
		"claude": "--dangerously-skip-permissions",
		"codex":  "--dangerously-bypass-approvals-and-sandbox",
	} {
		tool := cfg.Tools[name]
		plan, err := Assemble(name, tool, "fix the bug", "", false, "", "")
		if err != nil {
			t.Fatalf("%s Assemble: %v", name, err)
		}
		launched, err := WithSkipPermissions(tool, plan.Command, true)
		if err != nil {
			t.Fatalf("%s launch: %v", name, err)
		}
		if !strings.HasPrefix(launched, tool.Command+" ") || !strings.HasSuffix(launched, " "+flag) {
			t.Errorf("%s launch = %q, want it to end in %s", name, launched, flag)
		}
		revive, err := ReviveCommand(name, tool, "conv-1", "")
		if err != nil {
			t.Fatalf("%s ReviveCommand: %v", name, err)
		}
		revived, err := WithSkipPermissions(tool, revive, true)
		if err != nil {
			t.Fatalf("%s revive: %v", name, err)
		}
		if !strings.Contains(revived, "conv-1") || !strings.HasSuffix(revived, " "+flag) {
			t.Errorf("%s revive = %q, want the conversation resumed with %s", name, revived, flag)
		}
		if off, err := WithSkipPermissions(tool, plan.Command, false); err != nil || off != plan.Command {
			t.Errorf("%s with skip off = %q, %v; want the command unchanged", name, off, err)
		}
	}
}

// opencode has no flag for it, so a launch asking for one is refused rather
// than run with prompts the operator thought were off.
func TestSkipPermissionsIsRefusedForACLIWithoutTheFlag(t *testing.T) {
	cfg, err := config.LoadDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	tool := cfg.Tools["opencode"]
	if _, err := WithSkipPermissions(tool, tool.Command, true); err == nil || !strings.Contains(err.Error(), "skip_permissions_flag") {
		t.Fatalf("err = %v, want a refusal naming skip_permissions_flag", err)
	}
	if got, err := WithSkipPermissions(tool, tool.Command, false); err != nil || got != tool.Command {
		t.Fatalf("skip off = %q, %v; want the command unchanged", got, err)
	}
}
