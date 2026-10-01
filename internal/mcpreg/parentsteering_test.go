package mcpreg

import (
	"strings"
	"testing"
)

// Every CLI the board launches carries Rule 2 where it reads standing
// instructions, worded for what that CLI can verify, and in ASCII.
func TestLaunchSteeringCarriesTheParentRule(t *testing.T) {
	for _, style := range []string{"claude", "codex", "opencode"} {
		steering := launchSteering(style)
		if !strings.HasPrefix(steering, delegationSteering(style)) || !strings.Contains(steering, parentSteeringHeading) {
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
	if !strings.Contains(launchSteering("claude"), "hook adds a note") ||
		!strings.Contains(launchSteering("claude"), "relay attestation") {
		t.Error("claude's rule does not point at the verified hook note and the attestation")
	}
	for _, style := range []string{"codex", "opencode"} {
		steering := launchSteering(style)
		if strings.Contains(steering, "hook adds a note") || !strings.Contains(steering, "cannot verify a relayed approval") {
			t.Errorf("%s: a CLI with no prompt hook is told it has a verified note, or is not told approvals stay untrusted", style)
		}
	}
	if server, ok := ServerSteering("opencode"); !ok || !strings.Contains(server, parentSteeringHeading) {
		t.Error("opencode's server-carried steering lacks the parent rule")
	}
}
