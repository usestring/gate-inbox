package config

import "testing"

// A config written before Claude's errored rule was tightened carries the old
// pattern verbatim, which read indented tool output ("     error: failed to
// push") as the agent failing. Loading it upgrades the rule.
func TestClaudeErroredRuleUpgradesFromStoredConfig(t *testing.T) {
	def, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	stored := def.Tools["claude"]
	stored.Rules = append([]Rule(nil), stored.Rules...)
	for i := range stored.Rules {
		if stored.Rules[i].State == "errored" {
			stored.Rules[i].Pattern = claudeErroredIndented
		}
	}
	merged := mergeTool("claude", stored, def.Tools["claude"])
	found := false
	for _, r := range merged.Rules {
		if r.State != "errored" {
			continue
		}
		found = true
		if r.Pattern == claudeErroredIndented {
			t.Fatalf("stored rule %q was kept", r.Pattern)
		}
	}
	if !found {
		t.Fatal("errored rule dropped")
	}
}
