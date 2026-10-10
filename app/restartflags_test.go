package app

import (
	"context"
	"os"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/restartpresets"
)

func clearRestartFlagDefaults(t *testing.T) {
	t.Cleanup(func() {
		if _, err := restartpresets.UseDistribution(nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunLaysRestartFlagDefaultsUnderTheOperatorsFile(t *testing.T) {
	clearRestartFlagDefaults(t)
	t.Setenv(config.HomeEnv, t.TempDir())
	supplied := RestartFlag{Key: "b", Label: "with browser", Args: "--chrome"}
	if err := Run(context.Background(), []string{"--version"}, Options{RestartFlagDefaults: []RestartFlag{supplied}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(restartpresets.Path(dir), []byte(`[{"key":"d","label":"mine","args":"--foo"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := restartpresets.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := set.Get("b")
	if !ok || got.Args != supplied.Args {
		t.Fatalf("b = %+v, %v; want the build's preset", got, ok)
	}
	if _, ok := set.Get("d"); !ok {
		t.Fatal("the operator's own entry did not bind")
	}
}

func TestRunRefusesARestartFlagDefaultThatCannotBind(t *testing.T) {
	clearRestartFlagDefaults(t)
	t.Setenv(config.HomeEnv, t.TempDir())
	err := Run(context.Background(), []string{"--version"}, Options{RestartFlagDefaults: []RestartFlag{{Key: "b"}}})
	if err == nil {
		t.Fatal("Run = nil, want the empty entry refused")
	}
}
