package tmux

import (
	"strings"
	"testing"
)

func findingKeys(findings []ConfigFinding) []string {
	keys := make([]string, 0, len(findings))
	for _, finding := range findings {
		keys = append(keys, finding.Key)
	}
	return keys
}

func findingByKey(t *testing.T, findings []ConfigFinding, key string) ConfigFinding {
	t.Helper()
	for _, finding := range findings {
		if finding.Key == key {
			return finding
		}
	}
	t.Fatalf("no %s finding in %v", key, findingKeys(findings))
	return ConfigFinding{}
}

// The whole point of the check: a terminal tmux has not been told understands
// hyperlinks flattens every link the board draws, and says nothing about it.
func TestAMissingHyperlinksFeatureIsReportedWithTheLineThatFixesIt(t *testing.T) {
	findings := configFindings("bpaste,ccolour,clipboard,cstyle,focus,RGB,title", "xterm-256color", "on", "off")

	finding := findingByKey(t, findings, FindingHyperlinks)
	if finding.Fix != "set -as terminal-features 'xterm*:hyperlinks'" {
		t.Fatalf("fix line = %q", finding.Fix)
	}
	if !strings.Contains(finding.Detail, "hyperlinks") {
		t.Fatalf("detail never says what is missing: %q", finding.Detail)
	}
}

// The feature is claimed for the terminal family rather than the exact TERM,
// so the line keeps working when the same terminal reattaches under a
// different suffix.
func TestTheFixLineClaimsTheTerminalFamily(t *testing.T) {
	for _, row := range []struct{ termname, want string }{
		{"xterm-256color", "xterm*"},
		{"xterm-ghostty", "xterm*"},
		{"screen", "screen*"},
		{"", "*"},
	} {
		if got := termGlob(row.termname); got != row.want {
			t.Errorf("termGlob(%q) = %q, want %q", row.termname, got, row.want)
		}
	}
}

// A client that already has the feature is not told to add it again.
func TestAClientWithHyperlinksIsNotToldToTurnThemOn(t *testing.T) {
	findings := configFindings("clipboard,hyperlinks,RGB", "xterm-256color", "on", "off")

	for _, finding := range findings {
		if finding.Key == FindingHyperlinks {
			t.Fatalf("reported a missing feature the client has: %+v", finding)
		}
	}
}

// No features at all is no client, not a client missing every feature: an
// unattached session must not send the operator to edit a line that is right.
func TestNoAttachedClientReportsNothingClientScoped(t *testing.T) {
	findings := configFindings("", "", "external", "on")

	if keys := findingKeys(findings); len(keys) != 1 || keys[0] != FindingSetClipboard {
		t.Fatalf("findings without a client = %v, want the server option alone", keys)
	}
}

// set-clipboard is checked because the manager's copy-URL fallback is what it
// silently swallows, and "external" is the setting that does it.
func TestSetClipboardIsReportedUnlessItIsOn(t *testing.T) {
	for _, value := range []string{"external", "off"} {
		findings := configFindings("hyperlinks", "xterm-256color", value, "off")
		finding := findingByKey(t, findings, FindingSetClipboard)
		if finding.Fix != "set -s set-clipboard on" {
			t.Errorf("set-clipboard=%s fix line = %q", value, finding.Fix)
		}
		if !strings.Contains(finding.Detail, value) {
			t.Errorf("set-clipboard=%s detail never names the value: %q", value, finding.Detail)
		}
	}
	findings := configFindings("hyperlinks", "xterm-256color", "on", "off")
	for _, finding := range findings {
		if finding.Key == FindingSetClipboard {
			t.Fatalf("reported set-clipboard while it is on: %+v", finding)
		}
	}
}

// Mouse mode is a trade rather than a fault, so it carries no config line --
// and it is only worth raising where the links it intercepts exist at all.
func TestMouseModeIsRaisedOnlyWhereThereAreLinksToClick(t *testing.T) {
	finding := findingByKey(t, configFindings("hyperlinks", "xterm-256color", "on", "on"), FindingMouseClicks)
	if finding.Fix != "" {
		t.Fatalf("mouse finding carries a config line: %q", finding.Fix)
	}
	if !strings.Contains(finding.Detail, "Shift+click") {
		t.Fatalf("mouse finding never names the way through: %q", finding.Detail)
	}

	for _, finding := range configFindings("clipboard,RGB", "xterm-256color", "on", "on") {
		if finding.Key == FindingMouseClicks {
			t.Fatal("raised the mouse trade on a client whose links are flattened anyway")
		}
	}
}

// A board whose tmux is set up the way the operator's box is has nothing to
// say at startup.
func TestAHealthyConfigReportsNothing(t *testing.T) {
	if findings := configFindings("bpaste,clipboard,hyperlinks,RGB", "xterm-256color", "on", "off"); len(findings) != 0 {
		t.Fatalf("healthy config reported %v", findingKeys(findings))
	}
}

func TestClientTargetSplitsTheTmuxEnvironment(t *testing.T) {
	socket, session, ok := clientTarget("/tmp/tmux-1000/default,32832,219")
	if !ok || socket != "/tmp/tmux-1000/default" || session != "$219" {
		t.Fatalf("clientTarget = %q %q %v", socket, session, ok)
	}
	for _, value := range []string{"", "/tmp/sock", "/tmp/sock,32832", ",32832,219", "/tmp/sock,32832,"} {
		if _, _, ok := clientTarget(value); ok {
			t.Errorf("clientTarget(%q) claimed a target", value)
		}
	}
}

// A manager started outside tmux has no client whose settings could be wrong,
// and must not fork a tmux to find that out.
func TestNoTmuxEnvironmentChecksNothing(t *testing.T) {
	driver := &Driver{bin: "/nonexistent/tmux"}
	if findings := driver.checkConfig(""); findings != nil {
		t.Fatalf("checked a config outside tmux: %v", findingKeys(findings))
	}
}

// A read that fails says nothing about the operator's config, so it reports
// nothing rather than a card built from a partial answer.
func TestAFailedReadReportsNothing(t *testing.T) {
	driver := &Driver{bin: "/nonexistent/tmux"}
	if findings := driver.checkConfig("/tmp/sock,1,2"); findings != nil {
		t.Fatalf("reported findings from a failed read: %v", findingKeys(findings))
	}
}

func TestHasFeatureMatchesWholeNames(t *testing.T) {
	if !hasFeature("clipboard,hyperlinks,RGB", "hyperlinks") {
		t.Error("missed a feature that is present")
	}
	if hasFeature("clipboard,hyperlinksish", "hyperlinks") {
		t.Error("matched a feature name inside a longer one")
	}
	if hasFeature("", "hyperlinks") {
		t.Error("matched against an empty feature list")
	}
}
