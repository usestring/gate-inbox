package accounts

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func useChooser(t *testing.T, c *accountstest.Chooser) {
	t.Helper()
	t.Cleanup(UseChooser(c.Resolve))
}

var tokened = config.Tool{AccountEnv: "CLAUDE_CODE_OAUTH_TOKEN", AccountSecret: "SUB_{account}", AccountsCommand: "list"}

// With no extension choosing, a launch that names no account runs on the
// CLI's own login, and one that names an account runs on it.
func TestOwnLoginUnlessAnAccountIsNamed(t *testing.T) {
	st := testStore(t)
	chooser := &accountstest.Chooser{Account: "PICKED"}
	useChooser(t, chooser)
	if got, err := Select(st, tokened, "", Request{SessionID: "s1"}); err != nil || got != "" {
		t.Fatalf("own mode = %q %v", got, err)
	}
	if got, err := Select(st, tokened, " named ", Request{SessionID: "s1"}); err != nil || got != "NAMED" {
		t.Fatalf("named = %q %v", got, err)
	}
	if len(chooser.Requests()) != 0 {
		t.Fatal("the chooser was asked in own mode")
	}
}

func TestExtensionModeAsksTheChooser(t *testing.T) {
	st := testStore(t)
	if err := st.SetSetting(store.AccountRoutingSetting, Extension); err != nil {
		t.Fatal(err)
	}
	chooser := &accountstest.Chooser{Account: "picked"}
	useChooser(t, chooser)
	got, err := Select(st, tokened, "", Request{SessionID: "s1", ToolName: "claude", Reason: extension.LaunchSpawn})
	if err != nil || got != "PICKED" {
		t.Fatalf("Select = %q %v", got, err)
	}
	if _, err := Preview(st, tokened, "", Request{ToolName: "claude"}); err != nil {
		t.Fatal(err)
	}
	reqs := chooser.Requests()
	if len(reqs) != 2 || reqs[0].SessionID != "s1" || reqs[0].Tool.Secret != "SUB_{account}" || reqs[0].Preview || !reqs[1].Preview {
		t.Fatalf("requests = %+v", reqs)
	}
	if got, err := Select(st, tokened, "NAMED", Request{}); err != nil || got != "NAMED" {
		t.Fatalf("a named account still wins: %q %v", got, err)
	}
	if got, err := Select(st, config.Tool{}, "", Request{}); err != nil || got != "" || len(chooser.Requests()) != 2 {
		t.Fatalf("a CLI with no account settings asked the chooser: %q %v", got, err)
	}
	chooser.Err = errors.New("nothing usable")
	if _, err := Select(st, tokened, "", Request{}); err == nil {
		t.Fatal("a chooser's refusal did not refuse the launch")
	}
}

// A value an older build wrote for the setting is the extension's mode.
func TestAnyNonOwnModeIsTheExtensions(t *testing.T) {
	st := testStore(t)
	for value, want := range map[string]string{"": Own, "own": Own, "extension": Extension, "legacy": Extension} {
		if err := st.SetSetting(store.AccountRoutingSetting, value); err != nil {
			t.Fatal(err)
		}
		if got, err := Mode(st); err != nil || got != want {
			t.Errorf("Mode(%q) = %q %v, want %q", value, got, err, want)
		}
	}
}

func TestExtensionModeWithNoChooserRefuses(t *testing.T) {
	st := testStore(t)
	if err := st.SetSetting(store.AccountRoutingSetting, Extension); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(UseChooser(func() (extension.AccountChooser, error) { return nil, nil }))
	if _, err := Select(st, tokened, "", Request{}); !errors.Is(err, ErrNoChooser) {
		t.Fatalf("err = %v, want ErrNoChooser", err)
	}
	if ChooserAvailable() {
		t.Fatal("a build with no chooser reported one")
	}
}

// A migration asks the chooser whatever the mode, and without one runs on
// the CLI's own login.
func TestRouteAsksTheChooserOrFallsBackToOwnLogin(t *testing.T) {
	t.Cleanup(UseChooser(func() (extension.AccountChooser, error) { return nil, nil }))
	if got, err := Route(tokened, Request{SessionID: "m1"}); err != nil || got != "" {
		t.Fatalf("no chooser: %q %v", got, err)
	}
	chooser := &accountstest.Chooser{Account: "picked"}
	useChooser(t, chooser)
	got, err := Route(tokened, Request{SessionID: "m1", Reason: extension.LaunchMigrate, From: "src"})
	if err != nil || got != "PICKED" {
		t.Fatalf("Route = %q %v", got, err)
	}
	if reqs := chooser.Requests(); len(reqs) != 1 || reqs[0].Reason != extension.LaunchMigrate || reqs[0].From != "src" {
		t.Fatalf("requests = %+v", reqs)
	}
	if got, err := Route(config.Tool{}, Request{}); err != nil || got != "" {
		t.Fatalf("a CLI with no account settings: %q %v", got, err)
	}
}
