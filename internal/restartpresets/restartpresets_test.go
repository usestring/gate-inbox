package restartpresets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsCarryChrome(t *testing.T) {
	found := false
	for _, preset := range Defaults() {
		if preset.Args == "--chrome" {
			found = true
		}
	}
	if !found {
		t.Fatalf("defaults %v carry no --chrome preset", Defaults())
	}
}

func TestLoadWritesDefaultsOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Presets) == 0 {
		t.Fatal("first run wrote no presets")
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("first run wrote an empty file")
	}
	again, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Presets) != len(set.Presets) {
		t.Fatalf("reload presets = %d, want %d", len(again.Presets), len(set.Presets))
	}
}

func TestValidateRejectsBadKeys(t *testing.T) {
	set := validate([]Preset{
		{Key: "c", Label: "ok", Args: "--chrome"},
		{Key: "c", Label: "dup", Args: "--foo"},
		{Key: "", Label: "nokey", Args: "--bar"},
		{Key: "1", Label: "noletter", Args: "--baz"},
		{Key: "d", Label: "noargs", Args: "  "},
	})
	if len(set.Presets) != 1 {
		t.Fatalf("presets = %v, want the one valid entry", set.Presets)
	}
	if len(set.Problems) != 4 {
		t.Fatalf("problems = %v, want 4", set.Problems)
	}
}

func TestDistributionMergesUnderOwnFile(t *testing.T) {
	restore, err := UseDistribution([]Preset{{Key: "b", Label: "supplied", Args: "--supplied"}})
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	dir := t.TempDir()
	own := `[{"key":"c","label":"mine","args":"--chrome"}]`
	if err := os.WriteFile(filepath.Join(dir, "restart_flags.json"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Presets) != 2 {
		t.Fatalf("presets = %v, want mine plus the supplied one", set.Presets)
	}
}
