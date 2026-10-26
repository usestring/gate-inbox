package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/priority"
)

// Left out of the file, no tier carries a window: every session keeps the
// CLI's own, which is what the board did before the key existed.
func TestCompactWindowIsOffByDefault(t *testing.T) {
	cfg := loadFile(t, "")
	claude := cfg.Tools["claude"]
	if len(claude.CompactWindow) != 0 {
		t.Fatalf("default compact_window = %v, want none", claude.CompactWindow)
	}
	for _, tier := range append(slices.Clone(priority.Order), priority.Unset) {
		if got := claude.CompactWindowFor(tier); got != 0 {
			t.Fatalf("default window for %q = %d, want 0", tier, got)
		}
	}
	if !strings.Contains(defaultConfig, "# compact_window = { urgent = 600000, high = 300000 }\n") {
		t.Fatal("default config does not document compact_window")
	}
}

// A tier named in the table gets its window; one left out, and a session
// with no tier, get 0, the CLI's own.
func TestCompactWindowByTier(t *testing.T) {
	cfg := loadFile(t, "[tools.claude]\ncompact_window = { urgent = 600000, high = 300000 }\n")
	claude := cfg.Tools["claude"]
	for tier, want := range map[priority.Tier]int{
		priority.Urgent: 600000,
		priority.High:   300000,
		priority.Medium: 0,
		priority.Low:    0,
		priority.Unset:  0,
	} {
		if got := claude.CompactWindowFor(tier); got != want {
			t.Fatalf("window for %q = %d, want %d", tier, got, want)
		}
	}
	if claude.Command != "claude" {
		t.Fatalf("the rest of the block was not backfilled: command = %q", claude.Command)
	}
}

// A tier that is not one, or a window Claude Code would refuse, is named
// at load rather than written into a settings file.
func TestCompactWindowRejectsWhatClaudeWouldNot(t *testing.T) {
	for _, tc := range []struct{ name, table, want string }{
		{"unknown tier", `{ p0 = 600000 }`, `"p0" is not a tier`},
		{"empty tier", `{ "" = 600000 }`, `"" is not a tier`},
		{"below range", `{ high = 50000 }`, "compact_window.high = 50000"},
		{"above range", `{ urgent = 2000000 }`, "compact_window.urgent = 2000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := "[tools.claude]\ncompact_window = " + tc.table + "\n"
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(file), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadDir(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadDir err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// Left unset, a downgrade waits the default; set, it waits what was asked.
func TestCompactDowngradeDelay(t *testing.T) {
	if got := loadFile(t, "").Tools["claude"].CompactDowngradeDelayOrDefault(); got != DefaultCompactDowngradeDelay {
		t.Fatalf("default downgrade delay = %v, want %v", got, DefaultCompactDowngradeDelay)
	}
	cfg := loadFile(t, "[tools.claude]\ncompact_downgrade_delay = \"2m\"\n")
	if got := cfg.Tools["claude"].CompactDowngradeDelayOrDefault(); got != 2*time.Minute {
		t.Fatalf("downgrade delay = %v, want 2m", got)
	}
}
