package autoroute

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	got, err := commandQuota(context.Background(), "printf '%s' '"+string(payload)+"'")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Duration != 30*24*time.Hour {
		t.Fatalf("monthly quota = %+v, %v", got, err)
	}
}
