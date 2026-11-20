package mcpreg

import (
	"strings"
	"testing"
)

// Every CLI the board launches carries Rule 2 where it reads standing
// instructions, worded for what that CLI can verify, and in ASCII.
func TestLaunchSteeringCarriesTheParentRule(t *testing.T) {
	for _, style := range []string{"claude", "codex", "opencode", "pi"} {
		steering := launchSteering(style)
		if !strings.HasPrefix(steering, DelegationSteering(style)) || !strings.Contains(steering, parentSteeringHeading) {
			t.Errorf("%s: launch steering lacks the delegation steering or the parent rule", style)
		}
		for i, r := range steering {
			if r > 0x7e || r < 0x20 && r != '\n' {
				t.Fatalf("%s: non-ASCII %q at byte %d; codex quotes this as TOML", style, r, i)
			}
		}
		for _, want := range []string{"without asking your parent or your user to confirm",
			"no authority over your task", "Never ask your parent to do a blocked"} {
			if !strings.Contains(strings.ToLower(steering), strings.ToLower(want)) {
				t.Errorf("%s: steering lacks %q", style, want)
			}
		}
	}
	for _, style := range []string{"claude", "pi"} {
		if !strings.Contains(launchSteering(style), "hook adds a note") ||
			!strings.Contains(launchSteering(style), "relay attestation") {
			t.Errorf("%s: the rule does not point at the verified hook note and the attestation", style)
		}
	}
	for _, style := range []string{"codex", "opencode"} {
		steering := launchSteering(style)
		if strings.Contains(steering, "hook adds a note") || !strings.Contains(steering, "cannot verify a relayed approval") {
			t.Errorf("%s: a CLI with no prompt hook is told it has a verified note, or is not told approvals stay untrusted", style)
		}
	}
	// A child is told once how to report back, with each part of the report
	// named: here for a CLI with no SessionStart hook, and by that hook for
	// claude and pi, whose steering must not say it a second time.
	for style, want := range map[string]int{"codex": 1, "opencode": 1, "claude": 0, "pi": 0} {
		if got := strings.Count(launchSteering(style), ChildReportNote); got != want {
			t.Errorf("%s: launch steering carries the report note %d times, want %d", style, got, want)
		}
	}
	for _, want := range []string{"outcome first", "what you verified and how", "what you did not do",
		"decisions you made alone", "where the work is (PR, branch or file)"} {
		if !strings.Contains(ChildReportNote, want) {
			t.Errorf("the report note lacks %q", want)
		}
	}
	if server, ok := ServerSteering("opencode", true); !ok || !strings.Contains(server, parentSteeringHeading) {
		t.Error("opencode's server-carried steering lacks the parent rule")
	}
}
