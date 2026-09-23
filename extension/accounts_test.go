package extension_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// lender is a stub that shares accounts.
type lender struct{ stub }

func (l *lender) AccountPool() extension.AccountPool { return l }

func (*lender) Borrower(context.Context) (string, error) { return "OWNER", nil }
func (*lender) Owns(account, borrower string) bool       { return account == borrower }
func (*lender) Members(context.Context, extension.AccountTool) ([]string, error) {
	return nil, nil
}
func (*lender) Usage(context.Context, extension.AccountTool, string) (extension.AccountUsage, error) {
	return extension.AccountUsage{}, errors.New("no usage")
}

func TestABuildWithNoLenderHasNoPool(t *testing.T) {
	registry := mustRegistry(t, &stub{id: "plain"})
	pool, err := registry.AccountPool("", nil)
	if err != nil || pool != nil {
		t.Fatalf("pool = %v, %v; want none", pool, err)
	}
}

func TestTheEnabledLenderIsThePool(t *testing.T) {
	on := &lender{stub{id: "on"}}
	registry := mustRegistry(t, &stub{id: "plain"}, &lender{stub{id: "off", off: true}}, on)
	pool, err := registry.AccountPool("", nil)
	if err != nil || pool != on {
		t.Fatalf("pool = %v, %v; want the enabled lender", pool, err)
	}
}

func TestTwoEnabledLendersAreRefused(t *testing.T) {
	registry := mustRegistry(t, &lender{stub{id: "a"}}, &lender{stub{id: "b"}})
	if _, err := registry.AccountPool("", nil); err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Fatalf("got %v, want both lenders named", err)
	}
}

// A CLI command routing one launch configures the lender and nothing else.
func TestAnUnconfiguredRegistryConfiguresOnlyTheLender(t *testing.T) {
	plain, pooled := &stub{id: "plain"}, &lender{stub{id: "pooled"}}
	registry, err := extension.NewRegistry([]extension.Extension{plain, pooled})
	if err != nil {
		t.Fatal(err)
	}
	sections := map[string]map[string]any{"pooled": {"greeting": "hi"}, "plain": {"greeting": "no"}}
	pool, err := registry.AccountPool(t.TempDir(), sections)
	if err != nil || pool != pooled {
		t.Fatalf("pool = %v, %v", pool, err)
	}
	if plain.configured != nil {
		t.Fatal("an extension that shares no accounts was configured")
	}
	if pooled.settings.Greeting != "hi" {
		t.Fatalf("lender configured with %q, want its own section", pooled.settings.Greeting)
	}
	if _, err := pooled.configured.DataDir(); err != nil {
		t.Fatalf("lender has no data directory: %v", err)
	}
}

func TestALenderThatRefusesItsConfigIsNoPool(t *testing.T) {
	registry, err := extension.NewRegistry([]extension.Extension{&lender{stub{id: "pooled"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AccountPool("", map[string]map[string]any{"pooled": {"nope": 1}}); err == nil {
		t.Fatal("a lender with an unknown key supplied a pool")
	}
}

// nilLender is enabled but hands back a typed nil.
type nilLender struct{ stub }

func (*nilLender) AccountPool() extension.AccountPool { return (*lender)(nil) }

func TestAnEnabledLenderWithANilPoolIsAnError(t *testing.T) {
	registry := mustRegistry(t, &nilLender{stub{id: "empty"}})
	if pool, err := registry.AccountPool("", nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("pool = %v, %v; want an error naming the extension", pool, err)
	}
}
