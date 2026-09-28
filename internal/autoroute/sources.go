package autoroute

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
)

type wireWindow struct {
	Utilization *float64  `json:"utilization"`
	Percent     *float64  `json:"percent"`
	UsedPercent *float64  `json:"used_percent"`
	ResetsAt    time.Time `json:"resets_at"`
	ResetUnix   int64     `json:"reset_at"`
	Minutes     int       `json:"window_minutes"`
	Seconds     int       `json:"duration_seconds"`
}

func duration(name string, w wireWindow) time.Duration {
	if w.Seconds > 0 {
		return time.Duration(w.Seconds) * time.Second
	}
	if w.Minutes > 0 {
		return time.Duration(w.Minutes) * time.Minute
	}
	switch name {
	case "five_hour", "primary":
		return 5 * time.Hour
	case "seven_day", "weekly", "secondary":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	}
	if strings.HasPrefix(name, "seven_day_") {
		return 7 * 24 * time.Hour
	}
	return 0
}

func windows(wire map[string]wireWindow) []Window {
	out := make([]Window, 0, len(wire))
	for name, w := range wire {
		used := usedPercent(w)
		if used == nil {
			continue
		}
		reset := w.ResetsAt
		if reset.IsZero() && w.ResetUnix > 0 {
			reset = time.Unix(w.ResetUnix, 0)
		}
		out = append(out, Window{Used: *used, ResetsAt: reset, Duration: duration(name, w)})
	}
	return out
}

func usedPercent(w wireWindow) *float64 {
	if w.Utilization != nil {
		return w.Utilization
	}
	if w.Percent != nil {
		return w.Percent
	}
	return w.UsedPercent
}

func Reader(tools map[string]config.Tool) ReadFunc {
	return func(ctx context.Context, name string) (Reading, error) {
		tool, ok := tools[name]
		if !ok {
			return Reading{}, ErrNoQuota
		}
		if tool.QuotaCommand != "" {
			return commandQuota(ctx, tool.QuotaCommand)
		}
		switch name {
		case "claude":
			return claudeQuota(ctx)
		case "codex":
			return codexQuota(ctx)
		case "opencode":
			return opencodeQuota(ctx)
		default:
			return Reading{}, ErrNoQuota
		}
	}
}

func commandQuota(ctx context.Context, command string) (Reading, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	output, err := cmd.Output()
	if err != nil || len(output) > 1<<20 {
		return Reading{}, ErrNoQuota
	}
	var payload struct {
		ObservedAt time.Time             `json:"observed_at"`
		Windows    map[string]wireWindow `json:"windows"`
	}
	if json.Unmarshal(output, &payload) != nil {
		return Reading{}, ErrNoQuota
	}
	return Reading{ObservedAt: payload.ObservedAt, Windows: windows(payload.Windows)}, nil
}

func home() string {
	dir, _ := os.UserHomeDir()
	return dir
}

func claudeConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".claude")
}

func xdgDataHome() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".local", "share")
}

func getJSON(ctx context.Context, endpoint, token string, headers map[string]string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrNoQuota
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
}

func claudeToken(ctx context.Context) string {
	raw, err := os.ReadFile(filepath.Join(claudeConfigDir(), ".credentials.json"))
	if err != nil && runtime.GOOS == "darwin" {
		raw, err = exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	}
	if err != nil {
		return ""
	}
	var credentials struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(raw, &credentials) != nil || credentials.OAuth.AccessToken == "" {
		return ""
	}
	if credentials.OAuth.ExpiresAt > 0 && time.Now().UnixMilli() >= credentials.OAuth.ExpiresAt {
		return ""
	}
	return credentials.OAuth.AccessToken
}

func claudeQuota(ctx context.Context) (Reading, error) {
	token := claudeToken(ctx)
	if token == "" {
		return Reading{}, ErrNoQuota
	}
	var payload map[string]wireWindow
	if err := getJSON(ctx, "https://api.anthropic.com/api/oauth/usage", token, map[string]string{"anthropic-beta": "oauth-2025-04-20"}, &payload); err != nil {
		return Reading{}, err
	}
	return claudeReading(payload, time.Now())
}

func claudeReading(payload map[string]wireWindow, observedAt time.Time) (Reading, error) {
	selected := map[string]wireWindow{}
	for name, window := range payload {
		if name == "five_hour" || name == "seven_day" || strings.HasPrefix(name, "seven_day_") {
			selected[name] = window
		}
	}
	for _, name := range []string{"five_hour", "seven_day"} {
		window, ok := selected[name]
		if !ok || usedPercent(window) == nil {
			return Reading{}, ErrNoQuota
		}
	}
	return Reading{ObservedAt: observedAt, Windows: windows(selected)}, nil
}

type opencodeWindow struct {
	Percent  *float64  `json:"percent"`
	ResetsAt time.Time `json:"resetsAt"`
}

func opencodeQuota(ctx context.Context) (Reading, error) {
	raw, err := os.ReadFile(filepath.Join(xdgDataHome(), "opencode", "auth.json"))
	if err != nil {
		return Reading{}, ErrNoQuota
	}
	var auth map[string]struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(raw, &auth) != nil || auth["opencode-go"].Key == "" {
		return Reading{}, ErrNoQuota
	}
	var payload struct {
		Usage map[string]opencodeWindow `json:"usage"`
	}
	if err := getJSON(ctx, "https://opencode.ai/zen/go/v1/usage", auth["opencode-go"].Key, nil, &payload); err != nil {
		return Reading{}, err
	}
	return opencodeReading(payload.Usage, time.Now())
}

func opencodeReading(usage map[string]opencodeWindow, observedAt time.Time) (Reading, error) {
	selected := map[string]wireWindow{}
	for _, name := range []string{"weekly", "monthly"} {
		window, ok := usage[name]
		if !ok || window.Percent == nil {
			return Reading{}, ErrNoQuota
		}
		selected[name] = wireWindow{Percent: window.Percent, ResetsAt: window.ResetsAt}
	}
	return Reading{ObservedAt: observedAt, Windows: windows(selected)}, nil
}

func codexQuota(ctx context.Context) (Reading, error) {
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		root = filepath.Join(home(), ".codex")
	}
	type candidate struct {
		path     string
		modified time.Time
	}
	var files []candidate
	_ = filepath.WalkDir(filepath.Join(root, "sessions"), func(path string, entry os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || entry == nil || entry.IsDir() || !strings.HasPrefix(entry.Name(), "rollout-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err == nil && !info.ModTime().After(time.Now()) && time.Since(info.ModTime()) <= cacheTTL {
			files = append(files, candidate{path, info.ModTime()})
		}
		return nil
	})
	if len(files) == 0 {
		return Reading{}, ErrNoQuota
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modified.After(files[j].modified) })
	var best Reading
	for _, candidate := range files {
		if ctx.Err() != nil {
			return Reading{}, ErrNoQuota
		}
		reading, err := codexQuotaFile(ctx, candidate.path)
		if err == nil && reading.ObservedAt.After(best.ObservedAt) {
			best = reading
		}
	}
	if len(best.Windows) != 2 {
		return Reading{}, ErrNoQuota
	}
	return best, nil
}

func codexQuotaFile(ctx context.Context, path string) (Reading, error) {
	file, err := os.Open(path)
	if err != nil {
		return Reading{}, ErrNoQuota
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	type codexWindow struct {
		Used     *float64 `json:"used_percent"`
		ResetsAt int64    `json:"resets_at"`
		Minutes  int      `json:"window_minutes"`
	}
	type codexLimits struct {
		Primary   *codexWindow `json:"primary"`
		Secondary *codexWindow `json:"secondary"`
	}
	var best Reading
	for scanner.Scan() {
		if ctx.Err() != nil {
			return Reading{}, ErrNoQuota
		}
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"rate_limits"`)) {
			continue
		}
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
			Payload   struct {
				RateLimits *codexLimits `json:"rate_limits"`
				Info       struct {
					RateLimits *codexLimits `json:"rate_limits"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &entry) != nil || !entry.Timestamp.After(best.ObservedAt) {
			continue
		}
		limits := entry.Payload.RateLimits
		if limits == nil {
			limits = entry.Payload.Info.RateLimits
		}
		if limits == nil {
			continue
		}
		best = Reading{ObservedAt: entry.Timestamp}
		if limits.Primary != nil && limits.Secondary != nil && limits.Primary.Used != nil && limits.Secondary.Used != nil {
			best.Windows = []Window{
				{Used: *limits.Primary.Used, ResetsAt: time.Unix(limits.Primary.ResetsAt, 0), Duration: time.Duration(limits.Primary.Minutes) * time.Minute},
				{Used: *limits.Secondary.Used, ResetsAt: time.Unix(limits.Secondary.ResetsAt, 0), Duration: time.Duration(limits.Secondary.Minutes) * time.Minute},
			}
		}
	}
	if err := scanner.Err(); err != nil || best.ObservedAt.IsZero() {
		return Reading{}, errors.New("Codex quota snapshot unavailable")
	}
	return best, nil
}
