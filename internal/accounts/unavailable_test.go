package accounts

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

// disabled is what gcloud prints, after its exit status, for a secret whose
// latest version is disabled.
const disabled = "exit status 1: ERROR: (gcloud.secrets.versions.access) FAILED_PRECONDITION: Secret Version [projects/1/secrets/SUB_CAROL3/versions/3] is in DISABLED state."

// freshMarks gives a test its own unavailable and prefetched marks and
// clock.
func freshMarks(t *testing.T) *time.Time {
	t.Helper()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	marks.Lock()
	prevNow := marks.now
	marks.unavailable, marks.prefetched = map[string]time.Time{}, map[string]prefetch{}
	marks.now = func() time.Time { return now }
	marks.Unlock()
	t.Cleanup(func() {
		marks.Lock()
		marks.unavailable, marks.prefetched, marks.now = map[string]time.Time{}, map[string]prefetch{}, prevNow
		marks.Unlock()
	})
	return &now
}

// secrets stubs the secret store: every account reads as its own token
// except the ones failing with the given error.
func secrets(t *testing.T, failing map[string]error) *[]string {
	t.Helper()
	var mu sync.Mutex
	var ran []string
	stub(t, func(command string) (string, error) {
		mu.Lock()
		ran = append(ran, command)
		mu.Unlock()
		if command == "list" {
			return "SUB_CAROL3\nSUB_BOB2\n", nil
		}
		account := strings.TrimPrefix(strings.TrimPrefix(command, "read "), "SUB_")
		if err, ok := failing[account]; ok {
			return "", err
		}
		return "token-" + account, nil
	})
	return &ran
}

// A secret the store refuses for the account's own sake is unavailable, in
// one line naming the account and why, not the store's whole answer; a
// read that fails for any other reason is not.
func TestResolveCallsAnUnreadableSecretUnavailable(t *testing.T) {
	freshMarks(t)
	for stderr, want := range map[string]string{
		disabled: "account CAROL3 is unavailable: its secret version is disabled",
		"exit status 1: ERROR: (gcloud.secrets.versions.access) FAILED_PRECONDITION: Secret Version [x] is in DESTROYED state.":       "account CAROL3 is unavailable: its secret version is destroyed",
		"exit status 1: ERROR: (gcloud.secrets.versions.access) NOT_FOUND: Secret [x] not found or has no versions.":                  "account CAROL3 is unavailable: its secret or secret version does not exist",
		"exit status 1: ERROR: (gcloud.secrets.versions.access) PERMISSION_DENIED: Permission 'secretmanager.versions.access' denied": "account CAROL3 is unavailable: you may not read its secret",
	} {
		secrets(t, map[string]error{"CAROL3": errors.New(stderr)})
		_, err := Resolve(readable, "carol3")
		if !errors.Is(err, ErrUnavailable) || err.Error() != want {
			t.Errorf("%s:\n got %v\nwant %s", stderr, err, want)
		}
		var unavailable *UnavailableError
		if !errors.As(err, &unavailable) || unavailable.Err == nil || unavailable.Secret != "SUB_CAROL3" {
			t.Errorf("the store's answer is not kept: %#v", unavailable)
		}
	}
	for _, other := range []string{"no answer within 30s", "exit status 127: sh: 1: secrets: not found"} {
		secrets(t, map[string]error{"CAROL3": errors.New(other)})
		if _, err := Resolve(readable, "CAROL3"); err == nil || errors.Is(err, ErrUnavailable) {
			t.Errorf("%q read as unavailable: %v", other, err)
		}
	}
	stub(t, func(string) (string, error) { return " \n", nil })
	if _, err := Resolve(readable, "CAROL3"); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty secret: %v", err)
	}
}

// An account a read found unavailable leaves the list, so the picker stops
// offering it, until a read succeeds or the mark lapses.
func TestListHidesAnAccountFoundUnavailable(t *testing.T) {
	now := freshMarks(t)
	failing := map[string]error{"CAROL3": errors.New(disabled)}
	secrets(t, failing)
	if got, _ := List(readable); strings.Join(got, ",") != "CAROL3,BOB2" {
		t.Fatalf("before any read: %v", got)
	}
	if _, err := Resolve(readable, "CAROL3"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if got, _ := List(readable); strings.Join(got, ",") != "BOB2" {
		t.Fatalf("after a failed read: %v", got)
	}
	if !MarkedUnavailable(readable, "carol3") {
		t.Fatal("not marked")
	}
	*now = now.Add(unavailableFor)
	if got, _ := List(readable); strings.Join(got, ",") != "CAROL3,BOB2" {
		t.Fatalf("after the mark lapsed: %v", got)
	}
	if _, err := Resolve(readable, "CAROL3"); err == nil {
		t.Fatal("still disabled")
	}
	delete(failing, "CAROL3")
	if token, err := Resolve(readable, "CAROL3"); err != nil || token != "token-CAROL3" {
		t.Fatalf("re-enabled: %q %v", token, err)
	}
	if got, _ := List(readable); strings.Join(got, ",") != "CAROL3,BOB2" {
		t.Fatalf("after a good read: %v", got)
	}
}

// excluding answers in order, skipping whatever the request excludes when
// honor is set, and records what it was asked and told.
type excluding struct {
	order []string
	honor bool

	mu       sync.Mutex
	requests []extension.AccountRequest
	told     []string
}

func (c *excluding) ChooseAccount(_ context.Context, req extension.AccountRequest) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	for _, name := range c.order {
		if !c.honor || !containsFold(req.Exclude, name) {
			return name, nil
		}
	}
	return "", errors.New("nothing usable")
}

func (c *excluding) AccountUnavailable(_ context.Context, req extension.AccountRequest, account string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.told = append(c.told, req.SessionID+":"+account+":"+err.Error())
}

func containsFold(list []string, name string) bool {
	for _, s := range list {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

func extensionMode(t *testing.T) *store.Store {
	t.Helper()
	st := testStore(t)
	if err := st.SetSetting(store.AccountRoutingSetting, Extension); err != nil {
		t.Fatal(err)
	}
	return st
}

var readable = config.Tool{AccountEnv: "SUB_TOKEN", AccountSecret: "SUB_{account}", AccountCommand: "read {secret}", AccountsCommand: "list"}

// The chooser's pick of a disabled account does not refuse the launch: the
// chooser hears of it and is asked again without it, and the launch runs
// on its next pick, whose token the launch then reads only once.
func TestSelectAsksAgainWithoutAnUnavailablePick(t *testing.T) {
	freshMarks(t)
	st := extensionMode(t)
	ran := secrets(t, map[string]error{"CAROL3": errors.New(disabled)})
	c := &excluding{order: []string{"carol3", "bob2"}, honor: true}
	t.Cleanup(UseChooser(func() (extension.AccountChooser, error) { return c, nil }))
	warned := ""
	got, err := Select(st, readable, "", Request{SessionID: "s1", ToolName: "claude", Reason: extension.LaunchSpawn, Warn: func(why string) { warned = why }})
	if err != nil || got != "BOB2" {
		t.Fatalf("Select = %q %v", got, err)
	}
	if warned != "" {
		t.Fatalf("warned %q for a launch that found an account", warned)
	}
	if len(c.requests) != 2 || len(c.requests[0].Exclude) != 0 || strings.Join(c.requests[1].Exclude, ",") != "CAROL3" {
		t.Fatalf("requests = %+v", c.requests)
	}
	if len(c.told) != 1 || c.told[0] != "s1:CAROL3:account CAROL3 is unavailable: its secret version is disabled" {
		t.Fatalf("told = %v", c.told)
	}
	reads := len(*ran)
	if token, err := Resolve(readable, got); err != nil || token != "token-BOB2" || len(*ran) != reads {
		t.Fatalf("launch read %q %v, %d more reads", token, err, len(*ran)-reads)
	}
	// A preview after that skips the account without reading anything.
	reads = len(*ran)
	if got, err := Preview(st, readable, "", Request{ToolName: "claude"}); err != nil || got != "BOB2" || len(*ran) != reads {
		t.Fatalf("Preview = %q %v, %d reads", got, err, len(*ran)-reads)
	}
}

// A chooser that ignores the exclusion, or has nothing else, puts the
// launch on the CLI's own login with a one-line warning; a migration too.
func TestSelectFallsBackToOwnLoginWhenNoPickIsReadable(t *testing.T) {
	freshMarks(t)
	st := extensionMode(t)
	secrets(t, map[string]error{"CAROL3": errors.New(disabled)})
	// A chooser that answers the exclusion with an error is named in the
	// warning, so a timeout is not read as an empty pool.
	for name, tc := range map[string]struct {
		c    *excluding
		want string
	}{
		"ignores exclude": {&excluding{order: []string{"CAROL3"}}, "account CAROL3 is unavailable: its secret version is disabled"},
		"has no other":    {&excluding{order: []string{"CAROL3"}, honor: true}, "account CAROL3 is unavailable: its secret version is disabled; choosing an account: nothing usable"},
	} {
		c := tc.c
		restore := UseChooser(func() (extension.AccountChooser, error) { return c, nil })
		warned := ""
		req := Request{SessionID: "s1", ToolName: "claude", Warn: func(why string) { warned = why }}
		got, err := Select(st, readable, "", req)
		if err != nil || got != "" || warned != tc.want {
			t.Errorf("%s: Select = %q %v, warned %q", name, got, err, warned)
		}
		warned = ""
		req.Reason = extension.LaunchMigrate
		if got, err := Route(readable, req); err != nil || got != "" || warned == "" {
			t.Errorf("%s: Route = %q %v, warned %q", name, got, err, warned)
		}
		restore()
	}
}

// A named account is the operator's choice: Select takes it as given, and
// the launch's read refuses it in one line.
func TestANamedUnavailableAccountIsRefusedInOneLine(t *testing.T) {
	freshMarks(t)
	st := extensionMode(t)
	secrets(t, map[string]error{"CAROL3": errors.New(disabled)})
	c := &excluding{order: []string{"BOB2"}}
	t.Cleanup(UseChooser(func() (extension.AccountChooser, error) { return c, nil }))
	got, err := Select(st, readable, "carol3", Request{SessionID: "s1"})
	if err != nil || got != "CAROL3" || len(c.requests) != 0 {
		t.Fatalf("Select = %q %v, asked %d", got, err, len(c.requests))
	}
	_, err = Resolve(readable, got)
	if err == nil || err.Error() != "account CAROL3 is unavailable: its secret version is disabled" {
		t.Fatalf("Resolve = %v", err)
	}
}

// A read that fails for no reason of the account's leaves the pick alone:
// the launch's own read reports it.
func TestATransientReadFailureKeepsThePick(t *testing.T) {
	freshMarks(t)
	st := extensionMode(t)
	secrets(t, map[string]error{"CAROL3": errors.New("no answer within 30s")})
	c := &excluding{order: []string{"CAROL3", "BOB2"}, honor: true}
	t.Cleanup(UseChooser(func() (extension.AccountChooser, error) { return c, nil }))
	if got, err := Select(st, readable, "", Request{SessionID: "s1"}); err != nil || got != "CAROL3" || len(c.told) != 0 {
		t.Fatalf("Select = %q %v, told %v", got, err, c.told)
	}
}
