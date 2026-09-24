package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"github.com/usestring/gate-inbox/internal/tracing"
)

func quota(now time.Time, used float64, reset time.Duration) Snapshot {
	return Snapshot{At: now, Windows: map[string]*Window{
		"five_hour": {Utilization: &used, ResetsAt: now.Add(reset)},
		"seven_day": {Utilization: &used, ResetsAt: now.Add(7 * 24 * time.Hour)},
	}}
}

func TestQuotaEligibility(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name     string
		change   func(*Snapshot)
		eligible bool
	}{
		{"fresh", func(s *Snapshot) {}, true},
		{"stale", func(s *Snapshot) { s.At = now.Add(-2 * time.Minute) }, false},
		{"future", func(s *Snapshot) { s.At = now.Add(time.Minute) }, false},
		{"missing_week", func(s *Snapshot) { delete(s.Windows, "seven_day") }, false},
		{"unknown", func(s *Snapshot) { s.Windows["five_hour"].Utilization = nil }, false},
		{"expired", func(s *Snapshot) { s.Windows["five_hour"].ResetsAt = now.Add(-time.Second) }, false},
		{"exhausted", func(s *Snapshot) { v := 100.0; s.Windows["five_hour"].Utilization = &v }, false},
		{"week_reserve", func(s *Snapshot) { v := 86.0; s.Windows["seven_day"].Utilization = &v }, false},
		{"reset_soon", func(s *Snapshot) {
			v := 90.0
			s.Windows["five_hour"] = &Window{Utilization: &v, ResetsAt: now.Add(time.Minute)}
		}, true},
		{"unused", func(s *Snapshot) { v := 0.0; s.Windows["five_hour"] = &Window{Utilization: &v} }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := quota(now, 20, 5*time.Hour)
			tc.change(&s)
			if _, ok := s.pressure(now); ok != tc.eligible {
				t.Fatalf("eligible=%v, want %v", ok, tc.eligible)
			}
		})
	}
}

// usePool makes pool this process's account pool for the test.
func usePool(t *testing.T, pool *accountstest.Pool) *accountstest.Pool {
	t.Helper()
	t.Cleanup(UsePool(pool.Resolve))
	return pool
}

func routingStore(t *testing.T) (*store.Store, *accountstest.Pool) {
	t.Helper()
	pool := usePool(t, accountstest.LoggedInAs("OWNER"))
	s, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, pool
}

func TestSmartRoutingOwnFirstThenPersistentRoundRobin(t *testing.T) {
	st, pool := routingStore(t)
	st.SetSetting(store.DefaultAccountSetting, "owner")
	st.SetSetting(store.AccountRoutingSetting, Smart)
	tool := config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN", AccountSecret: "SUB_{account}", AccountsCommand: "list"}
	stub(t, func(string) (string, error) { return "SUB_OWNER\nSUB_C\nSUB_B\nSUB_B\nSUB_BAD\n", nil })
	ownerUsed := 20.0
	pool.UsageOf = func(name string) (extension.AccountUsage, error) {
		if name == "BAD" {
			return extension.AccountUsage{}, errors.New("unavailable")
		}
		used := 20.0
		if name == "OWNER" {
			used = ownerUsed
		}
		return accountstest.Quota(time.Now(), used, 5*time.Hour), nil
	}
	if got, err := Select(st, tool, "", "own-session"); err != nil || got != "OWNER" {
		t.Fatalf("%q %v", got, err)
	}
	ownerUsed = 100
	st.SetSetting("account_usage:v2:SUB_{account}:OWNER", "")
	for i, want := range []string{"B", "C", "B", "C"} {
		// A preview names the account the next Select takes, and takes
		// no turn itself.
		for range 2 {
			if got, err := Preview(st, tool, ""); err != nil || got != want {
				t.Fatalf("preview of turn %d: %q %v", i, got, err)
			}
		}
		got, err := Select(st, tool, "", fmt.Sprint(i))
		if err != nil || got != want {
			t.Fatalf("turn %d: %q %v", i, got, err)
		}
		if borrower, _ := st.Setting("account_borrower:" + fmt.Sprint(i)); borrower != "OWNER" {
			t.Fatal(borrower)
		}
	}
	if got, err := Select(st, tool, "PINNED", "pinned"); err != nil || got != "PINNED" {
		t.Fatalf("explicit: %q %v", got, err)
	}
}

func TestResetTimeChangesPoolPreference(t *testing.T) {
	st, pool := routingStore(t)
	stub(t, func(string) (string, error) { return "SUB_LATER\nSUB_SOON", nil })
	pool.UsageOf = func(name string) (extension.AccountUsage, error) {
		if name == "OWNER" {
			return extension.AccountUsage{}, errors.New("no quota")
		}
		reset := 5 * time.Hour
		if name == "SOON" {
			reset = time.Minute
		}
		u := accountstest.Quota(time.Now(), 80, reset)
		if name == "SOON" {
			week := u.Windows["seven_day"]
			week.ResetsAt = time.Now().Add(time.Minute)
			u.Windows["seven_day"] = week
		}
		return u, nil
	}
	got, _, err := selectSmart(st, config.Tool{AccountSecret: "SUB_{account}", AccountsCommand: "list"}, "OWNER", true)
	if err != nil || got != "SOON" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestOwnModeAndUnsupportedToolsNeverReadQuota(t *testing.T) {
	st, pool := routingStore(t)
	st.SetSetting(store.DefaultAccountSetting, "OWNER")
	pool.UsageOf = func(string) (extension.AccountUsage, error) {
		t.Fatal("unexpected quota read")
		return extension.AccountUsage{}, nil
	}
	if got, err := Select(st, config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN"}, "", "id"); err != nil || got != "" {
		t.Fatalf("%q %v", got, err)
	}
	st.SetSetting(store.AccountRoutingSetting, Smart)
	if got, err := Select(st, config.Tool{}, "", "id"); err != nil || got != "" {
		t.Fatalf("%q %v", got, err)
	}
	pool.Login = ""
	if _, err := Select(st, config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN"}, "", "id"); err == nil {
		t.Fatal("anonymous borrowing allowed")
	}
}

func TestConcurrentRotationAcrossConnections(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "state.db")
	a, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var wg sync.WaitGroup
	results := make(chan string, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := a
			if i%2 == 0 {
				st = b
			}
			name, err := st.NextAccount("claude", []string{"A", "B"})
			if err != nil {
				t.Error(err)
			}
			results <- name
		}()
	}
	wg.Wait()
	close(results)
	counts := map[string]int{}
	for name := range results {
		counts[name]++
	}
	if counts["A"] != 10 || counts["B"] != 10 {
		t.Fatal(counts)
	}
}

func TestExpiredCacheIsRefetchedAndFailureNeverBorrows(t *testing.T) {
	st, pool := routingStore(t)
	tool := config.Tool{AccountSecret: "SUB_{account}", AccountsCommand: "list"}
	stub(t, func(string) (string, error) { return "SUB_EMPTY\nSUB_BAD", nil })
	pool.UsageOf = func(name string) (extension.AccountUsage, error) {
		if name == "BAD" {
			return extension.AccountUsage{}, errors.New("unavailable")
		}
		return accountstest.Quota(time.Now(), 100, time.Hour), nil
	}
	expired := quota(time.Now(), 10, -time.Second)
	data, err := json.Marshal(expired)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSetting("account_usage:v2:SUB_{account}:OWNER", string(data))
	if got, _, err := selectSmart(st, tool, "OWNER", true); err == nil || got != "" {
		t.Fatalf("borrowed %q: %v", got, err)
	}
}

func TestLaunchTelemetryKeepsOriginalBorrower(t *testing.T) {
	st, pool := routingStore(t)
	st.SetSetting(store.DefaultAccountSetting, "OWNER")
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	tracer, err := tracing.Start("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tracer.Close() })
	tool := config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN"}
	if _, err := Select(st, tool, "LENDER", "borrowed"); err != nil {
		t.Fatal(err)
	}
	pool.Login = "NEW_OWNER"
	RecordLaunch(st, "borrowed", "claude", "LENDER")
	tracer.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"name":"account.launch"`, `"key":"account.borrower","value":{"stringValue":"OWNER"}`, `"key":"account.lender","value":{"stringValue":"LENDER"}`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	if strings.Contains(text, "NEW_OWNER") {
		t.Fatal("borrower was relabeled")
	}
}

func TestMonitorCollectsOneSnapshotPerLiveAccount(t *testing.T) {
	st, pool := routingStore(t)
	tool := config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN", AccountSecret: "SUB_{account}"}
	for _, row := range []store.Session{
		{ID: "first", Tool: "claude", Account: "LIVE"},
		{ID: "second", Tool: "claude", Account: "LIVE"},
		{ID: "dead", Tool: "claude", Account: "DEAD"},
	} {
		if err := st.CreateSession(row); err != nil {
			t.Fatal(err)
		}
	}
	pool.UsageOf = func(account string) (extension.AccountUsage, error) {
		if account != "LIVE" {
			t.Fatal(account)
		}
		return accountstest.Quota(time.Now(), 10, time.Hour), nil
	}
	for range 2 {
		if err := recordActiveUsage(st, map[string]config.Tool{"claude": tool}, func(id string) bool { return id != "dead" }, make(chan struct{})); err != nil {
			t.Fatal(err)
		}
	}
	if calls := pool.Reads(); calls != 1 {
		t.Fatalf("read %d times, want one cached account snapshot", calls)
	}
}
