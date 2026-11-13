package config

import (
	"errors"
	"strings"
	"testing"
)

// stubVersion answers lookPath and --version for one binary and counts the
// probes, so no real CLI runs.
func stubVersion(t *testing.T, out string, err error) *int {
	t.Helper()
	origLook, origRun := lookPath, runVersion
	calls := 0
	lookPath = func(name string) (string, error) { return "/stub/bin/" + name, nil }
	runVersion = func(string) (string, error) { calls++; return out, err }
	supported.Clear()
	t.Cleanup(func() {
		lookPath, runVersion = origLook, origRun
		supported.Clear()
	})
	return &calls
}

func TestCheckVersionRefusesPiBelowTheFloor(t *testing.T) {
	stubVersion(t, "0.85.1\n", nil)

	err := CheckVersion("pi --tui-mode regular")
	var outdated OutdatedToolError
	if !errors.As(err, &outdated) {
		t.Fatalf("got %v, want OutdatedToolError", err)
	}
	if outdated != (OutdatedToolError{Binary: "pi", Version: "0.85.1", Floor: "1.5.0"}) {
		t.Fatalf("refusal = %+v", outdated)
	}
	if !strings.Contains(err.Error(), "pi.dev/install.sh") {
		t.Fatalf("error %q should name the upgrade command", err)
	}
}

func TestCheckVersionAcceptsPiAtOrAboveTheFloor(t *testing.T) {
	for _, out := range []string{"1.5.0\n", "1.5.1\n", "pi v2.0.0\n", "1.10\n"} {
		stubVersion(t, out, nil)
		if err := CheckVersion("pi"); err != nil {
			t.Errorf("%q: %v", out, err)
		}
	}
}

// A probe that fails or prints no version lets the launch go ahead: the floor
// names a known-bad version and must not block on a broken probe.
func TestCheckVersionLetsAnUnreadableVersionLaunch(t *testing.T) {
	stubVersion(t, "", errors.New("exit status 1"))
	if err := CheckVersion("pi"); err != nil {
		t.Fatalf("failed probe: %v", err)
	}
	stubVersion(t, "pi\n", nil)
	if err := CheckVersion("pi"); err != nil {
		t.Fatalf("no version printed: %v", err)
	}
}

func TestCheckVersionSkipsToolsWithoutAFloor(t *testing.T) {
	calls := stubVersion(t, "0.0.1\n", nil)
	for _, command := range []string{"claude", "codex --yolo", ""} {
		if err := CheckVersion(command); err != nil {
			t.Fatalf("%q: %v", command, err)
		}
	}
	if *calls != 0 {
		t.Fatalf("probed %d times, want none", *calls)
	}
}

// A supported pi is probed once per board; a refused one is probed again on
// the next launch, so an upgrade made after the refusal is seen.
func TestCheckVersionRemembersOnlyAPass(t *testing.T) {
	calls := stubVersion(t, "0.85.1\n", nil)
	for range 2 {
		if CheckVersion("pi") == nil {
			t.Fatal("0.85.1 launched")
		}
	}
	if *calls != 2 {
		t.Fatalf("refusals probed %d times, want 2", *calls)
	}
	runVersion = func(string) (string, error) { *calls++; return "1.5.1\n", nil }
	for range 2 {
		if err := CheckVersion("pi"); err != nil {
			t.Fatalf("after the upgrade: %v", err)
		}
	}
	if *calls != 3 {
		t.Fatalf("probed %d times, want 3 (the pass is remembered)", *calls)
	}
}

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"1.4.9", "1.5.0", true},
		{"1.5", "1.5.0", false},
		// numeric, not lexical: 1.10 is newer than 1.5
		{"1.10.0", "1.5.0", false},
	} {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}
