package autoroute

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// commandQuotaWithin fails the test if commandQuota has not returned by the
// bound, so a regression shows up as a failure rather than a hung suite.
func commandQuotaWithin(t *testing.T, ctx context.Context, command string, bound time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := commandQuota(ctx, command, "")
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(bound):
		t.Fatalf("commandQuota(%q) still running after %v", command, bound)
		return nil
	}
}

func TestCustomQuotaCommandStopsADescendantHoldingStdout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := commandQuotaWithin(t, ctx, "sleep 30; echo late", 10*time.Second); err != ErrNoQuota {
		t.Fatalf("err = %v, want ErrNoQuota", err)
	}
}

func TestCustomQuotaCommandStopsAnUnboundedStream(t *testing.T) {
	if err := commandQuotaWithin(t, context.Background(), "yes", 10*time.Second); err != ErrNoQuota {
		t.Fatalf("err = %v, want ErrNoQuota", err)
	}
}

func TestCodexReadsRecentBackendWindows(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	dir := filepath.Join(root, "sessions", "2026", "09", "28")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	line, err := json.Marshal(map[string]any{
		"timestamp": now.Format(time.RFC3339Nano),
		"payload": map[string]any{"rate_limits": map[string]any{
			"primary":   map[string]any{"used_percent": 30, "resets_at": now.Add(4 * time.Hour).Unix(), "window_minutes": 300},
			"secondary": map[string]any{"used_percent": 20, "resets_at": now.Add(6 * 24 * time.Hour).Unix(), "window_minutes": 10080},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-test.jsonl"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-new-session.jsonl"), []byte(`{"type":"session_meta"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := codexQuota(context.Background())
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("Codex quota = %+v, %v", got, err)
	}
	if _, ok := Score(got, 0, time.Now()); !ok {
		t.Fatalf("recent Codex backend windows were rejected: %+v", got)
	}
}

func TestCodexRejectsLatestCreditsOnlySnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	windowed, _ := json.Marshal(map[string]any{
		"timestamp": now.Add(-time.Minute).Format(time.RFC3339Nano),
		"payload": map[string]any{"rate_limits": map[string]any{
			"primary":   map[string]any{"used_percent": 10, "resets_at": now.Add(4 * time.Hour).Unix(), "window_minutes": 300},
			"secondary": map[string]any{"used_percent": 10, "resets_at": now.Add(6 * 24 * time.Hour).Unix(), "window_minutes": 10080},
		}},
	})
	creditsOnly, _ := json.Marshal(map[string]any{
		"timestamp": now.Format(time.RFC3339Nano),
		"payload":   map[string]any{"rate_limits": map[string]any{"credits": map[string]any{"balance": 0}}},
	})
	if err := os.WriteFile(filepath.Join(dir, "rollout-old.jsonl"), append(windowed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-new.jsonl"), append(creditsOnly, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := codexQuota(context.Background()); err == nil {
		t.Fatalf("credits-only snapshot reused older windowed quota: %+v", got)
	}
}

func TestCodexQuotaHonorsCanceledContext(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := codexQuota(ctx); err == nil {
		t.Fatalf("canceled Codex quota read succeeded: %+v", got)
	}
}

func TestCustomQuotaCommandParsesMonthlyWindow(t *testing.T) {
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{
		"observed_at": now.Format(time.RFC3339Nano),
		"windows": map[string]any{"monthly": map[string]any{
			"utilization": 25, "resets_at": now.Add(25 * 24 * time.Hour).Format(time.RFC3339Nano),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := commandQuota(context.Background(), "printf '%s' '"+string(payload)+"'", "")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Duration != 30*24*time.Hour {
		t.Fatalf("monthly quota = %+v, %v", got, err)
	}
}

func TestClaudeReadingRequiresBothQuotaEnvelopes(t *testing.T) {
	now := time.Now().UTC()
	for _, sample := range []string{
		`{"five_hour":{"utilization":20},"seven_day":{"utilization":null}}`,
		`{"five_hour":{"utilization":null},"seven_day":{"utilization":20}}`,
		`{"five_hour":{"utilization":20}}`,
		`{"seven_day":{"utilization":20}}`,
	} {
		var payload map[string]wireWindow
		if err := json.Unmarshal([]byte(sample), &payload); err != nil {
			t.Fatal(err)
		}
		if got, err := claudeReading(payload, now); err == nil {
			t.Fatalf("partial Claude envelope %s accepted: %+v", sample, got)
		}
	}
	used := 20.0
	got, err := claudeReading(map[string]wireWindow{
		"five_hour":        {Utilization: &used, ResetsAt: now.Add(4 * time.Hour)},
		"seven_day":        {Utilization: &used, ResetsAt: now.Add(6 * 24 * time.Hour)},
		"seven_day_sonnet": {ResetsAt: now.Add(6 * 24 * time.Hour)},
	}, now)
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("complete Claude envelope = %+v, %v", got, err)
	}
}

func TestOpenCodeReadingRequiresWeeklyAndMonthlyQuota(t *testing.T) {
	now := time.Now().UTC()
	for _, sample := range []string{
		`{"weekly":{"percent":20},"monthly":{"percent":null}}`,
		`{"weekly":{"percent":null},"monthly":{"percent":20}}`,
		`{"weekly":{"percent":20}}`,
		`{"monthly":{"percent":20}}`,
	} {
		var usage map[string]opencodeWindow
		if err := json.Unmarshal([]byte(sample), &usage); err != nil {
			t.Fatal(err)
		}
		if got, err := opencodeReading(usage, now); err == nil {
			t.Fatalf("partial OpenCode envelope %s accepted: %+v", sample, got)
		}
	}
	used := 20.0
	got, err := opencodeReading(map[string]opencodeWindow{
		"weekly":  {Percent: &used, ResetsAt: now.Add(6 * 24 * time.Hour)},
		"monthly": {Percent: &used, ResetsAt: now.Add(25 * 24 * time.Hour)},
	}, now)
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("complete OpenCode envelope = %+v, %v", got, err)
	}
}

func TestCredentialPathsHonorDirectoryOverrides(t *testing.T) {
	t.Setenv("HOME", "/home/sample")
	t.Setenv("CLAUDE_CONFIG_DIR", "/srv/claude-config")
	t.Setenv("XDG_DATA_HOME", "/srv/xdg-data")
	if got := claudeConfigDir(); got != "/srv/claude-config" {
		t.Fatalf("claude config dir = %q, want CLAUDE_CONFIG_DIR", got)
	}
	if got := xdgDataHome(); got != "/srv/xdg-data" {
		t.Fatalf("data home = %q, want XDG_DATA_HOME", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	if got := claudeConfigDir(); got != filepath.Join("/home/sample", ".claude") {
		t.Fatalf("claude config dir = %q, want the ~/.claude default", got)
	}
	if got := xdgDataHome(); got != filepath.Join("/home/sample", ".local", "share") {
		t.Fatalf("data home = %q, want the ~/.local/share default", got)
	}
}

func TestCustomQuotaCommandRejectsAnIncompleteWindow(t *testing.T) {
	command := `printf '%s' '{"observed_at":"2026-09-28T18:00:00Z","windows":{"monthly":{"utilization":25,"resets_at":"2026-10-23T18:00:00Z","duration_seconds":2592000},"daily":{"utilization":null,"resets_at":"2026-09-29T18:00:00Z","duration_seconds":86400}}}'`
	if _, err := commandQuota(context.Background(), command, ""); err != ErrNoQuota {
		t.Fatalf("incomplete window err = %v, want ErrNoQuota", err)
	}
	if _, err := commandQuota(context.Background(), `printf '%s' '{"observed_at":"2026-09-28T18:00:00Z","windows":{}}'`, ""); err != ErrNoQuota {
		t.Fatalf("empty windows err = %v, want ErrNoQuota", err)
	}
}

func TestCommandQuotaClearsTheAccountEnv(t *testing.T) {
	t.Setenv("SAMPLE_ACCOUNT_TOKEN", "borrowed-token")
	command := `[ -z "$SAMPLE_ACCOUNT_TOKEN" ] && printf '%s' '{"windows":{"weekly":{"percent":20}}}'`
	if _, err := commandQuota(context.Background(), command, "SAMPLE_ACCOUNT_TOKEN"); err != nil {
		t.Fatalf("quota command saw the exported account token: %v", err)
	}
	if _, err := commandQuota(context.Background(), command, ""); err != ErrNoQuota {
		t.Fatalf("control run without an account env = %v, want the token visible", err)
	}
}
