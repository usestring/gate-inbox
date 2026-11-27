package accounts

import (
	"context"
	"errors"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/config"
)

// overrider is a chooser that also has the last word on every launch's
// account.
type overrider struct {
	accountstest.Chooser
	answer extension.LaunchAccount
	err    error
	panics bool
	asked  []extension.LaunchAccountRequest
}

func (o *overrider) Resolve() (extension.AccountChooser, error) { return o, nil }

func (o *overrider) OverrideLaunchAccount(_ context.Context, req extension.LaunchAccountRequest) (extension.LaunchAccount, error) {
	o.asked = append(o.asked, req)
	if o.panics {
		panic("broken overrider")
	}
	return o.answer, o.err
}

// A fork names its source's account and never reaches the chooser; the
// overrider still places it, and hears the fork's source. A revive keeps
// its account unless overridden, and own mode is overridden too.
func TestAnOverriderPlacesLaunchesThatNameTheirAccount(t *testing.T) {
	st := testStore(t)
	o := &overrider{answer: extension.LaunchAccount{Override: true, Account: "carol1", Warning: "moved off a closed account"}}
	t.Cleanup(UseChooser(o.Resolve))
	var warned string
	got, err := Select(st, tokened, "brian1", Request{SessionID: "fork", ToolName: "claude", Reason: extension.LaunchSpawn, From: "parent",
		Warn: func(w string) { warned = w }})
	if err != nil || got != "CAROL1" || warned != "moved off a closed account" {
		t.Fatalf("fork = %q %v, warned %q", got, err, warned)
	}
	a := o.asked[0]
	if a.SessionID != "fork" || a.From != "parent" || a.Account != "BRIAN1" || a.Reason != extension.LaunchSpawn || a.Tool.Env != tokened.AccountEnv {
		t.Fatalf("asked %+v", a)
	}
	if len(o.Requests()) != 0 {
		t.Fatal("the chooser was asked about a launch that named its account")
	}
	o.answer = extension.LaunchAccount{}
	if got, _ := Select(st, tokened, "brian1", Request{SessionID: "back", Reason: extension.LaunchRelaunch}); got != "BRIAN1" {
		t.Fatalf("a revive the overrider kept = %q", got)
	}
	o.answer = extension.LaunchAccount{Override: true, Account: ""}
	if got, _ := Select(st, tokened, "brian1", Request{SessionID: "back", Reason: extension.LaunchRelaunch}); got != "" {
		t.Fatalf("an override to the own login = %q", got)
	}
}

// A migration's chooser pick goes through the overrider too; a preview is
// never overridden.
func TestOverridesCoverMigrationsButNotPreviews(t *testing.T) {
	st := testStore(t)
	o := &overrider{Chooser: accountstest.Chooser{Account: "picked"}, answer: extension.LaunchAccount{Override: true, Account: "other"}}
	t.Cleanup(UseChooser(o.Resolve))
	if got, err := Route(tokened, Request{SessionID: "next", Reason: extension.LaunchMigrate, From: "old"}); err != nil || got != "OTHER" {
		t.Fatalf("migration = %q %v", got, err)
	}
	if got, _ := RoutePreview(tokened, Request{Reason: extension.LaunchMigrate}); got != "PICKED" {
		t.Fatalf("a migration preview was overridden: %q", got)
	}
	if got, _ := Preview(st, tokened, "brian1", Request{Reason: extension.LaunchSpawn}); got != "BRIAN1" {
		t.Fatalf("a launch preview was overridden: %q", got)
	}
	if len(o.asked) != 1 {
		t.Fatalf("asked %d times, want once", len(o.asked))
	}
}

// An overrider that fails or panics leaves the settled account; a CLI with
// no accounts is never asked.
func TestABrokenOverriderLeavesTheSettledAccount(t *testing.T) {
	st := testStore(t)
	for _, o := range []*overrider{
		{err: errors.New("unreachable"), answer: extension.LaunchAccount{Override: true, Account: "x"}},
		{panics: true},
	} {
		t.Cleanup(UseChooser(o.Resolve))
		if got, err := Select(st, tokened, "brian1", Request{SessionID: "s", Reason: extension.LaunchSpawn}); err != nil || got != "BRIAN1" {
			t.Fatalf("broken overrider: %q %v", got, err)
		}
	}
	o := &overrider{answer: extension.LaunchAccount{Override: true, Account: "x"}}
	t.Cleanup(UseChooser(o.Resolve))
	if got, _ := Select(st, config.Tool{}, "plain", Request{}); got != "plain" || len(o.asked) != 0 {
		t.Fatalf("a CLI without accounts = %q, asked %d", got, len(o.asked))
	}
}
