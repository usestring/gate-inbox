package compat

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

// TestAccountSelection records which subscription a launch is put on in
// each routing mode, with claude on a synthetic account setup, a fake
// secret store listing the accounts, a fake account pool standing in for
// the one a distribution's extension supplies, and usage snapshots seeded
// into the store the way the monitor leaves them. Snapshots are stamped
// with the current time and reset a month out, so every pressure the policy
// computes is the utilization itself and the answers do not move with the
// clock.
func TestAccountSelection(t *testing.T) {
	s := newScratch(t)
	s.writeFile(t, "config.toml", accountConfig)
	cfg, err := config.LoadDir(s.home)
	if err != nil {
		t.Fatal(err)
	}
	claude, codex := cfg.Tools["claude"], cfg.Tools["codex"]
	st, err := store.Open(filepath.Join(s.home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	usage := map[string]float64{}
	pool := accountstest.LoggedInAs(fakeBorrower)
	pool.UsageOf = func(account string) (extension.AccountUsage, error) {
		used, ok := usage[account]
		if !ok {
			return extension.AccountUsage{}, fmt.Errorf("no usage for %s", account)
		}
		return accountstest.Quota(time.Now(), used, 30*24*time.Hour), nil
	}
	defer accounts.UsePool(pool.Resolve)()

	var b strings.Builder
	selectOnce := func(label string, tool config.Tool, named, session string) {
		reads := pool.Reads()
		chosen, err := accounts.Select(st, tool, named, session)
		fmt.Fprintf(&b, "\n== %s\nchosen: %q\n", label, chosen)
		if err != nil {
			fmt.Fprintf(&b, "error: %v\n", err)
		}
		fmt.Fprintf(&b, "pool usage reads: %d\n", pool.Reads()-reads)
		fmt.Fprintf(&b, "secret store calls:\n%s", s.calls(t))
	}
	// cache leaves a reading per account in the store, written age ago.
	cache := func(age time.Duration, cached map[string]float64) {
		now := time.Now()
		for account, used := range cached {
			snap := accounts.Snapshot{At: now.Add(-age), Windows: map[string]*accounts.Window{
				"five_hour": {Utilization: &used, ResetsAt: now.Add(30 * 24 * time.Hour)},
				"seven_day": {Utilization: &used, ResetsAt: now.Add(30 * 24 * time.Hour)},
			}}
			encoded, err := json.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			key := "account_usage:v2:" + claude.AccountSecret + ":" + account
			if err := st.SetSetting(key, string(encoded)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// seed has the pool report each account's usage and leaves the same
	// reading fresh in the store's cache.
	seed := func(seeded map[string]float64) {
		clear(usage)
		for account, used := range seeded {
			usage[account] = used
		}
		cache(0, seeded)
	}

	selectOnce("a tool with no account_env passes the name through", codex, "ada1", "cafe0001")
	selectOnce("own mode, no account named", claude, "", "cafe0002")
	selectOnce("own mode, an account named", claude, "bob1", "cafe0003")

	if err := st.SetSetting(store.AccountRoutingSetting, accounts.Smart); err != nil {
		t.Fatal(err)
	}
	seed(map[string]float64{"ADA1": 40, "BOB1": 10, "CAT1": 10})
	selectOnce("smart mode, the login's own account has headroom", claude, "", "cafe0004")
	seed(map[string]float64{"ADA1": 90, "BOB1": 10, "CAT1": 10})
	for i := 1; i <= 3; i++ {
		selectOnce(fmt.Sprintf("smart mode, own account spent, pool round robin #%d", i), claude, "", fmt.Sprintf("cafe001%d", i))
	}
	seed(map[string]float64{"ADA1": 90, "BOB1": 90, "CAT1": 10})
	cache(time.Hour, map[string]float64{"ADA1": 0, "BOB1": 0, "CAT1": 0})
	selectOnce("smart mode, the cache is stale and the pool is read", claude, "", "cafe0019")
	seed(map[string]float64{"ADA1": 90, "BOB1": 90, "CAT1": 90})
	selectOnce("smart mode, every account spent", claude, "", "cafe0020")
	selectOnce("smart mode, an account named", claude, "cat1", "cafe0021")
	pool.Login = ""
	selectOnce("smart mode, the pool names no borrower", claude, "", "cafe0022")
	pool.Login = fakeBorrower
	other := claude
	other.AccountEnv = "EXAMPLE_TOKEN"
	selectOnce("smart mode, a tool whose usage nothing reports", other, "", "cafe0023")

	restore := accounts.UsePool(func() (extension.AccountPool, error) { return nil, nil })
	selectOnce("smart mode, a build that supplies no pool", claude, "", "cafe0024")
	selectOnce("smart mode with no pool, an account named", claude, "bob1", "cafe0025")
	if err := st.SetSetting(store.AccountRoutingSetting, accounts.Own); err != nil {
		t.Fatal(err)
	}
	selectOnce("own mode with no pool, no account named", claude, "", "cafe0026")
	restore()

	names, err := accounts.List(claude)
	fmt.Fprintf(&b, "\n== accounts listed for claude\n%q\n", names)
	if err != nil {
		fmt.Fprintf(&b, "error: %v\n", err)
	}
	fmt.Fprintf(&b, "secret store calls:\n%s", s.calls(t))

	// What the selection left in the store: which borrower each session
	// was attributed to, and the round-robin cursors. The usage caches are
	// listed by key only, since they hold the time they were written.
	b.WriteString("\n== settings left in the store\n")
	for _, key := range settingKeys(t, s) {
		value, err := st.Setting(key)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(key, "account_usage:") || strings.HasPrefix(key, "account_monitoring:") {
			value = "<timestamped snapshot>"
		}
		fmt.Fprintf(&b, "%s = %s\n", key, value)
	}
	golden(t, "accounts/selection.golden", s.redact(b.String()))
}

// settingKeys lists the settings table directly; the store has no call
// that enumerates it.
func settingKeys(t *testing.T, s *scratch) []string {
	t.Helper()
	db := openRaw(t, filepath.Join(s.home, "state.db"))
	rows, err := db.Query(`SELECT key FROM settings ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	return keys
}
