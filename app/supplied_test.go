package app

import (
	"context"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

// A build whose defaults the board cannot read is broken whichever face is
// asked, and says so before running any of them.
func TestRunRefusesDefaultsItCannotRead(t *testing.T) {
	t.Setenv(config.HomeEnv, t.TempDir())
	err := Run(context.Background(), []string{"--version"}, Options{ConfigDefaults: "[tools.claude]\naccount_evn = \"X\"\n"})
	if err == nil || !strings.Contains(err.Error(), "tools.claude.account_evn") {
		t.Fatalf("Run = %v, want the misspelt key refused", err)
	}
}

func TestRunRefusesDefaultsForAnExtensionTheBuildLacks(t *testing.T) {
	t.Setenv(config.HomeEnv, t.TempDir())
	err := Run(context.Background(), []string{"--version"}, Options{ConfigDefaults: "[extensions.stranger]\nx = 1\n"})
	want := "config defaults: [extensions] has section(s) no extension in this build owns: stranger (this build has: none)"
	if err == nil || err.Error() != want {
		t.Fatalf("Run = %v, want %q", err, want)
	}
}

func TestDistributionSuppliedMirrorsTheCoreList(t *testing.T) {
	got := DistributionSupplied()
	if len(got) != len(config.DistributionSupplied) {
		t.Fatalf("%d settings, want %d", len(got), len(config.DistributionSupplied))
	}
	for i, s := range config.DistributionSupplied {
		if want := (SuppliedSetting{Key: s.Key, Env: s.Env, Extension: s.Extension, Without: s.Without}); got[i] != want {
			t.Errorf("setting %d = %+v, want %+v", i, got[i], want)
		}
	}
	got[0].Key = "changed"
	if DistributionSupplied()[0].Key == "changed" {
		t.Fatal("a caller can edit the core's list")
	}
}
