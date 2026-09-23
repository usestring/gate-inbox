package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

func TestLoginRoutingPrefersAllOwnedAccountsBeforeBorrowing(t *testing.T) {
	st, pool := routingStore(t)
	st.SetSetting(store.DefaultAccountSetting, "OTHER")
	st.SetSetting(store.AccountRoutingSetting, Smart)
	stub(t, func(string) (string, error) { return "SUB_OWNER1\nSUB_OWNER2\nSUB_OTHER", nil })
	exhausted := false
	pool.UsageOf = func(name string) (extension.AccountUsage, error) {
		used := 10.0
		if name == "OWNER1" || exhausted {
			used = 100
		}
		if name == "OWNER2" && !exhausted {
			used = 70
		}
		if name == "OTHER" {
			used = 1
		}
		return accountstest.Quota(time.Now(), used, time.Hour), nil
	}
	tool := config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN", AccountSecret: "SUB_{account}", AccountsCommand: "list"}
	if got, err := Select(st, tool, "", "first"); err != nil || got != "OWNER2" {
		t.Fatalf("owned: %q %v", got, err)
	}
	exhausted = true
	st.SetSetting("account_usage:v2:SUB_{account}:OWNER2", "")
	if got, err := Select(st, tool, "", "child"); err != nil || got != "OTHER" {
		t.Fatalf("borrowed: %q %v", got, err)
	}
	if got, _ := st.Setting("account_borrower:child"); got != "OWNER" {
		t.Fatal(got)
	}
	st.SetSetting(store.AccountRoutingSetting, Own)
	if got, err := Select(st, tool, "", "local"); err != nil || got != "" {
		t.Fatalf("local: %q %v", got, err)
	}
}

func TestOwnedNumberedAccountIsNotReportedAsBorrowed(t *testing.T) {
	st, _ := routingStore(t)
	st.SetSetting("account_borrower:owned", "OWNER")
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tracer, err := tracing.Start("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer tracer.Close()
	RecordLaunch(st, "owned", "claude", "OWNER2")
	tracer.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"key":"account.shared","value":{"boolValue":false}`) {
		t.Fatalf("owned account marked shared: %s", data)
	}
}

func TestNoPoolLaunchesOnOwnLoginAndNamedAccounts(t *testing.T) {
	st, _ := routingStore(t)
	t.Cleanup(UsePool(func() (extension.AccountPool, error) { return nil, nil }))
	tool := config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN", AccountSecret: "SUB_{account}", AccountsCommand: "list"}
	if got, err := Select(st, tool, "", "own"); err != nil || got != "" {
		t.Fatalf("own login: %q %v", got, err)
	}
	if got, err := Select(st, tool, "alice1", "named"); err != nil || got != "ALICE1" {
		t.Fatalf("named: %q %v", got, err)
	}
	RecordLaunch(st, "named", "claude", "ALICE1")
	if got, _ := st.Setting("account_borrower:named"); got != "" {
		t.Fatalf("borrower %q recorded with no pool to name one", got)
	}
	st.SetSetting(store.AccountRoutingSetting, Smart)
	if _, err := Select(st, tool, "", "routed"); !errors.Is(err, ErrNoPool) {
		t.Fatalf("smart routing with no pool: %v", err)
	}
}

func TestAPoolThatCannotBeFoundIsNotMistakenForNone(t *testing.T) {
	st, _ := routingStore(t)
	broken := errors.New("[extensions.pool]: bad config")
	t.Cleanup(UsePool(func() (extension.AccountPool, error) { return nil, broken }))
	st.SetSetting(store.AccountRoutingSetting, Smart)
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tracer, err := tracing.Start("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer tracer.Close()
	if _, err := Select(st, config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN"}, "", "routed"); !errors.Is(err, broken) {
		t.Fatalf("got %v, want the pool's own error", err)
	}
	tracer.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"key":"account.reason","value":{"stringValue":"pool_unavailable"}`) {
		t.Fatalf("refusal not traced as pool_unavailable: %s", data)
	}
}
